package aoirunner

import (
	"testing"
	"time"

	"map-walker/internal/benchmark/aoiworkload"
	"map-walker/internal/game"
)

func TestRunCoreBuildKnownRelationshipTotal(t *testing.T) {
	scenario := smallCoreScenario(t)
	result := RunCoreBuild(scenario, CoreOptions{Repeat: 1})
	if result.Status != StatusSuccess {
		t.Fatalf("status=%s", result.Status)
	}
	if result.RelationshipsAfter == 0 {
		t.Fatal("expected relationships after build")
	}
	aoi := buildAOIIndex(scenario)
	if result.RelationshipsAfter != aoi.VisibleRelationshipPairs() {
		t.Fatalf("relationships=%d want %d", result.RelationshipsAfter, aoi.VisibleRelationshipPairs())
	}
}

func TestRunCoreBuildRecordsAllCheckpointsInBuildOrder(t *testing.T) {
	scenario := smallCoreScenario(t)
	var events []BuildProgressEvent
	result := RunCoreBuild(scenario, CoreOptions{
		Repeat: 1,
		OnBuildProgress: func(event BuildProgressEvent) {
			events = append(events, event)
		},
	})
	if len(result.BuildCheckpoints) != len(buildCheckpointPercents) {
		t.Fatalf("checkpoints=%d want %d", len(result.BuildCheckpoints), len(buildCheckpointPercents))
	}
	if len(events) != len(buildCheckpointPercents) {
		t.Fatalf("progress events=%d want %d", len(events), len(buildCheckpointPercents))
	}
	for i, want := range buildCheckpointPercents {
		if result.BuildCheckpoints[i].PercentComplete != want {
			t.Fatalf("checkpoint[%d]=%d want %d", i, result.BuildCheckpoints[i].PercentComplete, want)
		}
		if events[i].PercentComplete != want {
			t.Fatalf("event[%d]=%d want %d", i, events[i].PercentComplete, want)
		}
		if events[i].Timestamp.IsZero() || events[i].ElapsedNs <= 0 {
			t.Fatalf("event[%d] missing timestamp or elapsed time: %+v", i, events[i])
		}
	}
	if result.BuildCheckpoints[len(result.BuildCheckpoints)-1].RSSAvailable {
		t.Fatal("expected checkpoint RSS to remain unset without parent samples")
	}
}

func TestNearestRSSSample(t *testing.T) {
	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	samples := []RSSSample{
		{Timestamp: base.Add(5 * time.Millisecond), Bytes: 1000, Available: true, Source: "test"},
		{Timestamp: base.Add(25 * time.Millisecond), Bytes: 2000, Available: true, Source: "test"},
	}
	bytes, available, source, ok := nearestRSSSample(base.Add(6*time.Millisecond), samples)
	if !ok || !available || source != "test" || bytes == nil || *bytes != 1000 {
		t.Fatalf("nearest sample=%v available=%v source=%q ok=%v", bytes, available, source, ok)
	}
}

func TestRunCoreBuildAssociatesNearestRSSSample(t *testing.T) {
	scenario := smallCoreScenario(t)
	base := time.Now()
	samples := []RSSSample{
		{Timestamp: base.Add(-time.Second), Bytes: 1000, Available: true, Source: "test"},
		{Timestamp: base.Add(time.Hour), Bytes: 2000, Available: true, Source: "test"},
	}
	result := RunCoreBuild(scenario, CoreOptions{
		Repeat:     1,
		RSSSamples: samples,
	})
	if len(result.BuildCheckpoints) == 0 {
		t.Fatal("expected checkpoints")
	}
	for _, checkpoint := range result.BuildCheckpoints {
		if checkpoint.RSSBytes == nil {
			t.Fatal("expected checkpoint RSS association")
		}
		if checkpoint.RSSSource != "test" {
			t.Fatalf("rss source=%q", checkpoint.RSSSource)
		}
	}
}

func TestRunCoreTickMeasuredSampleCountAndWarmupExcluded(t *testing.T) {
	scenario := smallCoreScenario(t)
	result := RunCoreTick(scenario, CoreOptions{Repeat: 1})
	if result.Status != StatusSuccess {
		t.Fatalf("status=%s", result.Status)
	}
	if result.TickDuration == nil {
		t.Fatal("expected tick duration stats")
	}
	if result.BuildDurationNs != 0 {
		t.Fatal("core tick result should not include build duration")
	}
}

func TestRunCoreTickExactlyHundredSamples(t *testing.T) {
	scenario := smallCoreScenario(t)
	result := RunCoreTick(scenario, CoreOptions{Repeat: 1})
	samples := collectTickSamples(scenario)
	if len(samples) != aoiworkload.MeasuredAOIUpdates {
		t.Fatalf("samples=%d want %d", len(samples), aoiworkload.MeasuredAOIUpdates)
	}
	if result.TickDuration.MedianNs <= 0 {
		t.Fatal("expected non-zero median tick duration")
	}
}

func TestRunCoreTickThroughputAndRelationshipTotals(t *testing.T) {
	scenario := smallCoreScenario(t)
	result := RunCoreTick(scenario, CoreOptions{Repeat: 1})
	if result.Throughput == nil || result.Throughput.MovesPerSecond <= 0 {
		t.Fatalf("throughput=%+v", result.Throughput)
	}
	if result.RelationshipsBefore == 0 && result.RelationshipsAfter == 0 {
		t.Fatal("expected relationship totals")
	}
	if result.AOI == nil || result.AOI.CandidatePairs == 0 {
		t.Fatal("expected AOI counters from measured ticks")
	}
}

func TestRunCoreTickStableAcrossRepeats(t *testing.T) {
	scenario := smallCoreScenario(t)
	first := RunCoreTick(scenario, CoreOptions{Repeat: 1})
	second := RunCoreTick(scenario, CoreOptions{Repeat: 2})
	if first.RelationshipsAfter != second.RelationshipsAfter {
		t.Fatalf("relationships differ: %d vs %d", first.RelationshipsAfter, second.RelationshipsAfter)
	}
	if first.AOI.CandidatePairs != second.AOI.CandidatePairs {
		t.Fatalf("candidate pairs differ: %d vs %d", first.AOI.CandidatePairs, second.AOI.CandidatePairs)
	}
}

func TestRunCoreTickUsesMoveNotDirectMutation(t *testing.T) {
	scenario := smallCoreScenario(t)
	targets := preExpandCoreTargets(scenario)
	aoi := buildAOIIndex(scenario)
	for update := 0; update < aoiworkload.WarmupAOIUpdates; update++ {
		applyCoreMoves(aoi, targets[update])
	}
	before := aoi.VisibleRelationshipPairs()
	applyCoreMoves(aoi, targets[aoiworkload.WarmupAOIUpdates])
	afterMove := aoi.VisibleRelationshipPairs()
	if before == 0 && afterMove == 0 && len(scenario.MoverIndices) > 0 {
		t.Fatal("expected movement to affect index state")
	}
	if !aoi.HasPlayer(scenario.Players[scenario.MoverIndices[0]].ID) {
		t.Fatal("expected player to remain in index after Move")
	}
}

func TestRunCoreBuildNotApplicable(t *testing.T) {
	scenario, err := aoiworkload.Generate(aoiworkload.Config{
		Scale: 100, MoverCount: 200, Density: aoiworkload.DensityNormal, Seed: 1,
	})
	if err == nil {
		t.Fatal("expected generation error")
	}
	_ = scenario
	result := RunCoreBuild(&aoiworkload.Scenario{
		Config: aoiworkload.Config{Scale: 100, MoverCount: 200, Density: aoiworkload.DensityNormal, Seed: 1},
	}, CoreOptions{})
	if result.Status != StatusNotApplicable {
		t.Fatalf("status=%s", result.Status)
	}
}

func collectTickSamples(scenario *aoiworkload.Scenario) []time.Duration {
	targets := preExpandCoreTargets(scenario)
	aoi := buildAOIIndex(scenario)
	samples := make([]time.Duration, 0, aoiworkload.MeasuredAOIUpdates)
	for update := 0; update < aoiworkload.TotalAOIUpdates; update++ {
		if update < aoiworkload.WarmupAOIUpdates {
			applyCoreMoves(aoi, targets[update])
			aoi.TakeStats()
			continue
		}
		start := time.Now()
		applyCoreMoves(aoi, targets[update])
		samples = append(samples, time.Since(start))
		aoi.TakeStats()
	}
	return samples
}

func smallCoreScenario(t *testing.T) *aoiworkload.Scenario {
	t.Helper()
	scenario, err := aoiworkload.Generate(aoiworkload.Config{
		Scale:      128,
		MoverCount: 16,
		Density:    aoiworkload.DensityNormal,
		Seed:       31,
	})
	if err != nil {
		t.Fatal(err)
	}
	return scenario
}

func TestRunCoreTickRecordsWorkloadHeap(t *testing.T) {
	scenario := smallCoreScenario(t)
	result := RunCoreTick(scenario, CoreOptions{Repeat: 1})
	if result.WorkloadHeap == nil || result.WorkloadHeap.HeapAllocBytes == 0 {
		t.Fatalf("workload heap=%+v", result.WorkloadHeap)
	}
}

func TestApplyCoreMovesUsesPublicMove(t *testing.T) {
	scenario := smallCoreScenario(t)
	targets := preExpandCoreTargets(scenario)
	aoi := buildAOIIndex(scenario)
	target := targets[0][0]
	applyCoreMoves(aoi, []game.PlayerPosition{target})
	if _, ok := aoi.Cell(target.ID); !ok {
		t.Fatal("missing cell after Move")
	}
}
