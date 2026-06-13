package aoirunner

import (
	"time"

	"map-walker/internal/benchmark/aoiworkload"
	"map-walker/internal/game"
)

type RSSSample struct {
	Timestamp time.Time
	Bytes     int64
	Available bool
	Source    string
}

type BuildProgressEvent struct {
	Timestamp       time.Time
	PercentComplete int
	ElapsedNs       int64
}

type CoreOptions struct {
	Repeat          int
	Environment     EnvironmentCaptureOptions
	RSSSamples      []RSSSample
	OnBuildProgress func(BuildProgressEvent)
}

var buildCheckpointPercents = []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

func RunCoreBuild(scenario *aoiworkload.Scenario, opts CoreOptions) Result {
	result := baseCoreResult(scenario, ModeBuild, opts)
	if !scenario.Config.IsApplicable() {
		result.Status = StatusNotApplicable
		result.Phase = PhaseGeneration
		return result
	}

	aoi := game.NewAOIIndex(scenario.AOIConfig)
	memBefore := CaptureMemSnapshot()
	rssReader := NewRSSReader()
	start := time.Now()
	result.Phase = PhaseBuild

	checkpoints := make([]BuildCheckpoint, 0, len(buildCheckpointPercents))
	nextCheckpoint := 0
	playerCount := len(scenario.BuildOrder)

	for inserted := 1; inserted <= playerCount; inserted++ {
		playerIdx := scenario.BuildOrder[inserted-1]
		player := scenario.Players[playerIdx]
		aoi.Insert(player.ID, player.Lat, player.Lng)

		for nextCheckpoint < len(buildCheckpointPercents) &&
			inserted*100 >= playerCount*buildCheckpointPercents[nextCheckpoint] {
			elapsed := time.Since(start)
			event := BuildProgressEvent{
				Timestamp:       start.Add(elapsed),
				PercentComplete: buildCheckpointPercents[nextCheckpoint],
				ElapsedNs:       elapsed.Nanoseconds(),
			}
			if opts.OnBuildProgress != nil {
				opts.OnBuildProgress(event)
			}
			checkpoint := BuildCheckpoint{
				PercentComplete: event.PercentComplete,
				ElapsedNs:       event.ElapsedNs,
			}
			if rssBytes, available, source, ok := nearestRSSSample(event.Timestamp, opts.RSSSamples); ok {
				checkpoint.RSSBytes = rssBytes
				checkpoint.RSSAvailable = available
				checkpoint.RSSSource = source
			}
			checkpoints = append(checkpoints, checkpoint)
			nextCheckpoint++
		}
	}

	buildDuration := time.Since(start)
	memAfter := CaptureMemSnapshot()
	rssReader.Sample()

	result.Status = StatusSuccess
	result.Phase = PhaseBuild
	result.ElapsedNs = buildDuration.Nanoseconds()
	result.BuildDurationNs = buildDuration.Nanoseconds()
	result.BuildCheckpoints = checkpoints
	result.RelationshipsAfter = aoi.VisibleRelationshipPairs()
	result.Heap = heapPtr(HeapDelta(memBefore, memAfter))
	result.GC = gcPtr(GCDelta(memBefore, memAfter))
	result.RSS = rssPtr(rssReader.Snapshot())
	result.Throughput = &ThroughputStats{
		Class:          MetricPrimary,
		MovesPerSecond: float64(playerCount) / buildDuration.Seconds(),
	}
	return result
}

func RunCoreTick(scenario *aoiworkload.Scenario, opts CoreOptions) Result {
	result := baseCoreResult(scenario, ModeCoreTick, opts)
	if !scenario.Config.IsApplicable() {
		result.Status = StatusNotApplicable
		result.Phase = PhaseGeneration
		return result
	}

	memBefore := CaptureMemSnapshot()
	rssReader := NewRSSReader()

	targets := preExpandCoreTargets(scenario)
	workloadMem := CaptureMemSnapshot()
	result.WorkloadHeap = heapPtr(HeapSnapshotFromMem(workloadMem))

	aoi := buildAOIIndex(scenario)
	result.Phase = PhaseWarmup

	tickSamples := make([]time.Duration, 0, aoiworkload.MeasuredAOIUpdates)
	var measuredAOI AOICounters
	var measuredElapsed time.Duration
	totalMoves := 0

	for update := 0; update < aoiworkload.TotalAOIUpdates; update++ {
		if update == aoiworkload.WarmupAOIUpdates {
			result.RelationshipsBefore = aoi.VisibleRelationshipPairs()
			result.Phase = PhaseMeasuredTicks
		}

		updateTargets := targets[update]
		if update >= aoiworkload.WarmupAOIUpdates {
			tickStart := time.Now()
			applyCoreMoves(aoi, updateTargets)
			tickElapsed := time.Since(tickStart)
			tickSamples = append(tickSamples, tickElapsed)
			measuredElapsed += tickElapsed
			totalMoves += len(updateTargets)

			stats := aoi.TakeStats()
			measuredAOI.CandidatePairs += stats.CandidatePairs
			measuredAOI.DistanceChecks += stats.DistanceChecks
			measuredAOI.RelationshipsEntered += stats.RelationshipsEntered
			measuredAOI.RelationshipsLeft += stats.RelationshipsLeft
			rssReader.Sample()
		} else {
			applyCoreMoves(aoi, updateTargets)
			aoi.TakeStats()
		}
	}

	memAfter := CaptureMemSnapshot()
	rssReader.Sample()

	tickDuration := DurationStatsFromSamples(tickSamples)
	result.Status = StatusSuccess
	result.Phase = PhaseMeasuredTicks
	result.ElapsedNs = measuredElapsed.Nanoseconds()
	result.TickDuration = &tickDuration
	result.Throughput = &ThroughputStats{
		Class:          MetricPrimary,
		MovesPerSecond: ThroughputFromMoves(totalMoves, measuredElapsed).MovesPerSecond,
	}
	result.VisibilityChurn = visibilityChurnPtr(VisibilityChurnFromWorkload(scenario.VisibilityChurn))
	result.RelationshipsAfter = aoi.VisibleRelationshipPairs()
	measuredAOI.Class = MetricDiagnostic
	result.AOI = &measuredAOI
	result.Heap = heapPtr(HeapDelta(memBefore, memAfter))
	result.GC = gcPtr(GCDelta(memBefore, memAfter))
	result.RSS = rssPtr(rssReader.Snapshot())
	return result
}

func baseCoreResult(scenario *aoiworkload.Scenario, mode Mode, opts CoreOptions) Result {
	return Result{
		Identity:    ScenarioIdentityFromConfig(mode, scenario.Config, opts.Repeat),
		Environment: CaptureEnvironment(opts.Environment),
	}
}

func buildAOIIndex(scenario *aoiworkload.Scenario) *game.AOIIndex {
	aoi := game.NewAOIIndex(scenario.AOIConfig)
	for _, playerIdx := range scenario.BuildOrder {
		player := scenario.Players[playerIdx]
		aoi.Insert(player.ID, player.Lat, player.Lng)
	}
	return aoi
}

func preExpandCoreTargets(scenario *aoiworkload.Scenario) [][]game.PlayerPosition {
	expander := aoiworkload.NewScheduleExpander(scenario)
	targets := make([][]game.PlayerPosition, aoiworkload.TotalAOIUpdates)
	for update := 0; update < aoiworkload.TotalAOIUpdates; update++ {
		expanded := expander.ExpandUpdate(update)
		targets[update] = append([]game.PlayerPosition(nil), expanded.CoreTargets...)
	}
	return targets
}

func applyCoreMoves(aoi *game.AOIIndex, targets []game.PlayerPosition) {
	for _, target := range targets {
		aoi.Move(target.ID, target.Lat, target.Lng)
	}
}

func nearestRSSSample(eventTime time.Time, samples []RSSSample) (*int64, bool, string, bool) {
	if len(samples) == 0 {
		return nil, false, "", false
	}
	best := samples[0]
	bestDelta := absDuration(eventTime.Sub(best.Timestamp))
	for _, sample := range samples[1:] {
		delta := absDuration(eventTime.Sub(sample.Timestamp))
		if delta < bestDelta {
			best = sample
			bestDelta = delta
		}
	}
	if !best.Available {
		return nil, false, best.Source, true
	}
	bytes := best.Bytes
	return &bytes, true, best.Source, true
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func heapPtr(snapshot HeapSnapshot) *HeapSnapshot {
	return &snapshot
}

func gcPtr(snapshot GCSnapshot) *GCSnapshot {
	return &snapshot
}

func rssPtr(snapshot RSSSnapshot) *RSSSnapshot {
	return &snapshot
}

func visibilityChurnPtr(metric VisibilityChurnMetric) *VisibilityChurnMetric {
	return &metric
}
