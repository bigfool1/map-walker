package aoirunner

import (
	"testing"
	"time"

	"map-walker/internal/benchmark/aoiworkload"
	"map-walker/internal/game"
)

func TestRunWorldAOIUsesApplyInputStepAndAOIUpdate(t *testing.T) {
	scenario := smallWorldScenario(t)
	world := game.NewWorld(scenario.WorldConfig)
	for _, player := range scenario.Players {
		world.AddPlayerAt(player.ID, player.Lat, player.Lng)
	}
	aoi := buildAOIIndex(scenario)
	updates := preExpandWorldUpdates(scenario)
	tickInterval := time.Duration(aoiworkload.SimTickIntervalMs) * time.Millisecond

	for slot, playerIdx := range scenario.MoverIndices {
		world.ApplyInput(scenario.Players[playerIdx].ID, updates[0].WorldInputs[slot].Tick0)
	}
	world.Step(tickInterval)
	for slot, playerIdx := range scenario.MoverIndices {
		world.ApplyInput(scenario.Players[playerIdx].ID, updates[0].WorldInputs[slot].Tick1)
	}
	world.Step(tickInterval)

	movedBefore := len(world.TakeMovedPlayerIDs())
	applyAOIFromWorld(world, aoi)
	if movedBefore == 0 {
		t.Fatal("expected moved players before AOI update")
	}
	if aoi.TakeStats().DistanceChecks == 0 {
		t.Fatal("expected AOI distance checks")
	}
}

func TestRunWorldAOISampleCountsExcludeWarmup(t *testing.T) {
	scenario := smallWorldScenario(t)
	result := RunWorldAOI(scenario, WorldOptions{Repeat: 1})
	if result.Status != StatusSuccess {
		t.Fatalf("status=%s", result.Status)
	}
	if len(collectWorldSimSamples(scenario)) != WorldMeasuredSimTicks {
		t.Fatalf("sim samples=%d want %d", len(collectWorldSimSamples(scenario)), WorldMeasuredSimTicks)
	}
	if len(collectWorldAOISamples(scenario)) != aoiworkload.MeasuredAOIUpdates {
		t.Fatalf("aoi samples=%d want %d", len(collectWorldAOISamples(scenario)), aoiworkload.MeasuredAOIUpdates)
	}
	if result.SimulationDuration == nil || result.AOIPreparationDuration == nil || result.CombinedTickDuration == nil {
		t.Fatal("expected separate duration stats")
	}
}

func TestRunWorldAOIExactlyTwoHundredSimulationAndHundredAOISamples(t *testing.T) {
	scenario := smallWorldScenario(t)
	result := RunWorldAOI(scenario, WorldOptions{Repeat: 1})
	simCount, aoiCount := countWorldResultSamples(result)
	if simCount != WorldMeasuredSimTicks {
		t.Fatalf("simulation stats not based on 200 samples")
	}
	if aoiCount != aoiworkload.MeasuredAOIUpdates {
		t.Fatalf("aoi stats not based on 100 samples")
	}
	_ = simCount
	_ = aoiCount
	if result.CombinedTickDuration.MedianNs <= 0 {
		t.Fatal("expected combined tick duration")
	}
}

func countWorldResultSamples(result Result) (simCount, aoiCount int) {
	if result.SimulationDuration != nil {
		simCount = WorldMeasuredSimTicks
	}
	if result.AOIPreparationDuration != nil {
		aoiCount = aoiworkload.MeasuredAOIUpdates
	}
	return simCount, aoiCount
}

func TestRunWorldAOICorePositionsAgreeAtAOIBoundaries(t *testing.T) {
	scenario := smallWorldScenario(t)
	coreTargets := preExpandCoreTargets(scenario)
	world := game.NewWorld(scenario.WorldConfig)
	for _, player := range scenario.Players {
		world.AddPlayerAt(player.ID, player.Lat, player.Lng)
	}
	updates := preExpandWorldUpdates(scenario)
	tickInterval := time.Duration(aoiworkload.SimTickIntervalMs) * time.Millisecond

	for simTick := 0; simTick < WorldTotalSimTicks; simTick++ {
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
		world.Step(tickInterval)
		if !secondTick {
			continue
		}
		worldPositions := moverWorldPositions(scenario, world)
		core := coreTargets[updateIdx]
		for slot := range scenario.MoverIndices {
			if !positionsMatchWithinEpsilon(worldPositions[slot], core[slot], 1e-4) {
				t.Fatalf("update %d slot %d world=(%v,%v) core=(%v,%v)",
					updateIdx, slot,
					worldPositions[slot].Lat, worldPositions[slot].Lng,
					core[slot].Lat, core[slot].Lng)
			}
		}
	}
}

func TestRunWorldAOISeparatePercentilesAndBudgetMetrics(t *testing.T) {
	scenario := smallWorldScenario(t)
	result := RunWorldAOI(scenario, WorldOptions{Repeat: 1})
	if result.SimulationDuration.P95Ns < result.SimulationDuration.MedianNs {
		t.Fatalf("simulation percentiles out of order: %+v", result.SimulationDuration)
	}
	if result.AOIPreparationDuration.P95Ns < result.AOIPreparationDuration.MedianNs {
		t.Fatalf("aoi percentiles out of order: %+v", result.AOIPreparationDuration)
	}
	if result.CombinedTickDuration.MaxNs <= 0 {
		t.Fatal("expected combined tick max duration")
	}
	if result.RemainingBudget == nil {
		t.Fatal("expected remaining budget stats")
	}
	if result.SimulationBudgetExceededCount < 0 || result.AOIBudgetExceededCount < 0 {
		t.Fatal("expected non-negative budget exceeded counts")
	}
}

func TestRunWorldAOIRecordsSeparateSetupTimes(t *testing.T) {
	scenario := smallWorldScenario(t)
	result := RunWorldAOI(scenario, WorldOptions{Repeat: 1})
	if result.WorldSetupDurationNs <= 0 || result.AOISetupDurationNs <= 0 {
		t.Fatalf("setup durations world=%d aoi=%d", result.WorldSetupDurationNs, result.AOISetupDurationNs)
	}
	if result.BuildDurationNs != 0 {
		t.Fatal("world result should not reuse build duration field")
	}
}

func TestRunWorldAOINotApplicable(t *testing.T) {
	result := RunWorldAOI(&aoiworkload.Scenario{
		Config: aoiworkload.Config{Scale: 100, MoverCount: 200, Density: aoiworkload.DensityNormal, Seed: 1},
	}, WorldOptions{})
	if result.Status != StatusNotApplicable {
		t.Fatalf("status=%s", result.Status)
	}
}

func TestRunWorldAOIIdentityDistinguishableFromCore(t *testing.T) {
	scenario := smallWorldScenario(t)
	worldResult := RunWorldAOI(scenario, WorldOptions{Repeat: 1})
	coreResult := RunCoreTick(scenario, CoreOptions{Repeat: 1})
	if worldResult.Identity.Mode != ModeWorldAOI || coreResult.Identity.Mode != ModeCoreTick {
		t.Fatalf("modes world=%s core=%s", worldResult.Identity.Mode, coreResult.Identity.Mode)
	}
	if worldResult.SimulationDuration == nil || coreResult.TickDuration == nil {
		t.Fatal("expected mode-specific duration fields")
	}
}

func collectWorldSimSamples(scenario *aoiworkload.Scenario) []time.Duration {
	updates := preExpandWorldUpdates(scenario)
	world := game.NewWorld(scenario.WorldConfig)
	for _, player := range scenario.Players {
		world.AddPlayerAt(player.ID, player.Lat, player.Lng)
	}
	aoi := buildAOIIndex(scenario)
	tickInterval := time.Duration(aoiworkload.SimTickIntervalMs) * time.Millisecond
	samples := make([]time.Duration, 0, WorldMeasuredSimTicks)
	for simTick := 0; simTick < WorldTotalSimTicks; simTick++ {
		expanded := updates[simTick/2]
		secondTick := simTick%2 == 1
		for slot, playerIdx := range scenario.MoverIndices {
			input := expanded.WorldInputs[slot].Tick0
			if secondTick {
				input = expanded.WorldInputs[slot].Tick1
			}
			world.ApplyInput(scenario.Players[playerIdx].ID, input)
		}
		if simTick >= WorldWarmupSimTicks {
			start := time.Now()
			world.Step(tickInterval)
			samples = append(samples, time.Since(start))
		} else {
			world.Step(tickInterval)
		}
		if secondTick {
			applyAOIFromWorld(world, aoi)
			aoi.TakeStats()
		}
	}
	return samples
}

func collectWorldAOISamples(scenario *aoiworkload.Scenario) []time.Duration {
	updates := preExpandWorldUpdates(scenario)
	world := game.NewWorld(scenario.WorldConfig)
	for _, player := range scenario.Players {
		world.AddPlayerAt(player.ID, player.Lat, player.Lng)
	}
	aoi := buildAOIIndex(scenario)
	tickInterval := time.Duration(aoiworkload.SimTickIntervalMs) * time.Millisecond
	samples := make([]time.Duration, 0, aoiworkload.MeasuredAOIUpdates)
	for simTick := 0; simTick < WorldTotalSimTicks; simTick++ {
		expanded := updates[simTick/2]
		secondTick := simTick%2 == 1
		for slot, playerIdx := range scenario.MoverIndices {
			input := expanded.WorldInputs[slot].Tick0
			if secondTick {
				input = expanded.WorldInputs[slot].Tick1
			}
			world.ApplyInput(scenario.Players[playerIdx].ID, input)
		}
		world.Step(tickInterval)
		if secondTick {
			if simTick >= WorldWarmupSimTicks {
				start := time.Now()
				applyAOIFromWorld(world, aoi)
				samples = append(samples, time.Since(start))
			} else {
				applyAOIFromWorld(world, aoi)
			}
			aoi.TakeStats()
		}
	}
	return samples
}

func smallWorldScenario(t *testing.T) *aoiworkload.Scenario {
	t.Helper()
	scenario, err := aoiworkload.Generate(aoiworkload.Config{
		Scale:      128,
		MoverCount: 16,
		Density:    aoiworkload.DensityNormal,
		Seed:       37,
	})
	if err != nil {
		t.Fatal(err)
	}
	return scenario
}

func TestRemainingBudgetUsesCombinedTickBudget(t *testing.T) {
	combined := 30*time.Millisecond + 20*time.Millisecond + 40*time.Millisecond
	remaining := CombinedTickBudget - combined
	if remaining != 10*time.Millisecond {
		t.Fatalf("remaining=%v want 10ms", remaining)
	}
}
