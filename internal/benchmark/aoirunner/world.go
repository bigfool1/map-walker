package aoirunner

import (
	"math"
	"time"

	"map-walker/internal/benchmark/aoiworkload"
	"map-walker/internal/game"
)

const (
	WorldWarmupSimTicks    = 40
	WorldMeasuredSimTicks  = 200
	WorldTotalSimTicks     = WorldWarmupSimTicks + WorldMeasuredSimTicks
	SimulationTickBudget   = 50 * time.Millisecond
	AOIPreparationBudget   = 100 * time.Millisecond
	CombinedTickBudget     = 100 * time.Millisecond
)

type WorldOptions struct {
	Repeat      int
	Environment EnvironmentCaptureOptions
}

type expandedWorldUpdate struct {
	CoreTargets []game.PlayerPosition
	WorldInputs []aoiworkload.MoverWorldInputs
}

func RunWorldAOI(scenario *aoiworkload.Scenario, opts WorldOptions) Result {
	result := Result{
		Identity:    ScenarioIdentityFromConfig(ModeWorldAOI, scenario.Config, opts.Repeat),
		Environment: CaptureEnvironment(opts.Environment),
	}
	if !scenario.Config.IsApplicable() {
		result.Status = StatusNotApplicable
		result.Phase = PhaseGeneration
		return result
	}

	memBefore := CaptureMemSnapshot()
	rssReader := NewRSSReader()

	updates := preExpandWorldUpdates(scenario)
	workloadMem := CaptureMemSnapshot()
	result.WorkloadHeap = heapPtr(HeapSnapshotFromMem(workloadMem))

	worldSetupStart := time.Now()
	world := game.NewWorld(scenario.WorldConfig)
	for _, player := range scenario.Players {
		world.AddPlayerAt(player.ID, player.Lat, player.Lng)
	}
	result.WorldSetupDurationNs = time.Since(worldSetupStart).Nanoseconds()

	aoiSetupStart := time.Now()
	aoi := buildAOIIndex(scenario)
	result.AOISetupDurationNs = time.Since(aoiSetupStart).Nanoseconds()

	tickInterval := time.Duration(aoiworkload.SimTickIntervalMs) * time.Millisecond
	simSamples := make([]time.Duration, 0, WorldMeasuredSimTicks)
	aoiSamples := make([]time.Duration, 0, aoiworkload.MeasuredAOIUpdates)
	combinedSamples := make([]time.Duration, 0, aoiworkload.MeasuredAOIUpdates)
	remainingSamples := make([]time.Duration, 0, aoiworkload.MeasuredAOIUpdates)
	var measuredAOI AOICounters
	var simBudgetExceeded int
	var aoiBudgetExceeded int
	var pairSimElapsed time.Duration

	result.Phase = PhaseWarmup
	for simTick := 0; simTick < WorldTotalSimTicks; simTick++ {
		if simTick == WorldWarmupSimTicks {
			result.RelationshipsBefore = aoi.VisibleRelationshipPairs()
			result.Phase = PhaseMeasuredTicks
		}

		updateIdx := simTick / 2
		secondTick := simTick%2 == 1
		expanded := updates[updateIdx]
		for slot, playerIdx := range scenario.MoverIndices {
			playerID := scenario.Players[playerIdx].ID
			input := expanded.WorldInputs[slot].Tick0
			if secondTick {
				input = expanded.WorldInputs[slot].Tick1
			}
			world.ApplyInput(playerID, input)
		}

		measured := simTick >= WorldWarmupSimTicks
		var simElapsed time.Duration
		if measured {
			simStart := time.Now()
			world.Step(tickInterval)
			simElapsed = time.Since(simStart)
			simSamples = append(simSamples, simElapsed)
			if simElapsed > SimulationTickBudget {
				simBudgetExceeded++
			}
			rssReader.Sample()
		} else {
			world.Step(tickInterval)
		}

		if !secondTick {
			pairSimElapsed = simElapsed
			continue
		}

		var aoiElapsed time.Duration
		if measured {
			aoiStart := time.Now()
			applyAOIFromWorld(world, aoi)
			aoiElapsed = time.Since(aoiStart)
			aoiSamples = append(aoiSamples, aoiElapsed)
			if aoiElapsed > AOIPreparationBudget {
				aoiBudgetExceeded++
			}

			combined := pairSimElapsed + simElapsed + aoiElapsed
			combinedSamples = append(combinedSamples, combined)
			remaining := CombinedTickBudget - combined
			if remaining < 0 {
				remaining = 0
			}
			remainingSamples = append(remainingSamples, remaining)

			stats := aoi.TakeStats()
			measuredAOI.CandidatePairs += stats.CandidatePairs
			measuredAOI.DistanceChecks += stats.DistanceChecks
			measuredAOI.RelationshipsEntered += stats.RelationshipsEntered
			measuredAOI.RelationshipsLeft += stats.RelationshipsLeft
			rssReader.Sample()
		} else {
			applyAOIFromWorld(world, aoi)
			aoi.TakeStats()
		}
	}

	memAfter := CaptureMemSnapshot()
	rssReader.Sample()

	simulationDuration := DurationStatsFromSamples(simSamples)
	aoiDuration := DurationStatsFromSamples(aoiSamples)
	combinedDuration := DurationStatsFromSamples(combinedSamples)
	remainingBudget := DurationStatsFromSamples(remainingSamples)

	result.Status = StatusSuccess
	result.Phase = PhaseMeasuredTicks
	result.ElapsedNs = sumDurations(combinedSamples).Nanoseconds()
	result.SimulationDuration = &simulationDuration
	result.AOIPreparationDuration = &aoiDuration
	result.CombinedTickDuration = &combinedDuration
	result.RemainingBudget = &remainingBudget
	result.SimulationBudgetExceededCount = simBudgetExceeded
	result.AOIBudgetExceededCount = aoiBudgetExceeded
	result.VisibilityChurn = visibilityChurnPtr(VisibilityChurnFromWorkload(scenario.VisibilityChurn))
	result.RelationshipsAfter = aoi.VisibleRelationshipPairs()
	measuredAOI.Class = MetricDiagnostic
	result.AOI = &measuredAOI
	result.Heap = heapPtr(HeapDelta(memBefore, memAfter))
	result.GC = gcPtr(GCDelta(memBefore, memAfter))
	result.RSS = rssPtr(rssReader.Snapshot())
	result.Throughput = &ThroughputStats{
		Class: MetricPrimary,
		MovesPerSecond: ThroughputFromMoves(
			len(scenario.MoverIndices)*aoiworkload.MeasuredAOIUpdates,
			sumDurations(combinedSamples),
		).MovesPerSecond,
	}
	return result
}

func preExpandWorldUpdates(scenario *aoiworkload.Scenario) []expandedWorldUpdate {
	expander := aoiworkload.NewScheduleExpander(scenario)
	updates := make([]expandedWorldUpdate, aoiworkload.TotalAOIUpdates)
	for update := 0; update < aoiworkload.TotalAOIUpdates; update++ {
		expanded := expander.ExpandUpdate(update)
		updates[update] = expandedWorldUpdate{
			CoreTargets: append([]game.PlayerPosition(nil), expanded.CoreTargets...),
			WorldInputs: append([]aoiworkload.MoverWorldInputs(nil), expanded.WorldInputs...),
		}
	}
	return updates
}

func applyAOIFromWorld(world *game.World, aoi *game.AOIIndex) {
	movedIDs := world.TakeMovedPlayerIDs()
	positions := world.PlayerPositions(movedIDs)
	for _, position := range positions {
		aoi.Move(position.ID, position.Lat, position.Lng)
	}
}

func sumDurations(samples []time.Duration) time.Duration {
	var total time.Duration
	for _, sample := range samples {
		total += sample
	}
	return total
}

func moverWorldPositions(scenario *aoiworkload.Scenario, world *game.World) []game.PlayerPosition {
	positions := make([]game.PlayerPosition, len(scenario.MoverIndices))
	for slot, playerIdx := range scenario.MoverIndices {
		playerID := scenario.Players[playerIdx].ID
		pos, ok := world.PlayerPosition(playerID)
		if !ok {
			continue
		}
		positions[slot] = pos
	}
	return positions
}

func positionsMatchWithinEpsilon(a, b game.PlayerPosition, epsilon float64) bool {
	return math.Abs(a.Lat-b.Lat) <= epsilon && math.Abs(a.Lng-b.Lng) <= epsilon
}
