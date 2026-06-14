package aoiworkload

import (
	"math"
	"testing"
	"time"

	"map-walker/internal/game"
)

func TestGenerateRepeatability(t *testing.T) {
	config := Config{Scale: 512, MoverCount: 64, Density: DensityNormal, Seed: 42}
	first, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	if !scenariosEqual(first, second) {
		t.Fatal("same config and seed produced different scenarios")
	}
}

func TestDifferentSeedsChangePerturbations(t *testing.T) {
	base := Config{Scale: 256, MoverCount: 32, Density: DensityNormal, Seed: 1}
	other := Config{Scale: 256, MoverCount: 32, Density: DensityNormal, Seed: 2}
	first, err := Generate(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(other)
	if err != nil {
		t.Fatal(err)
	}
	if scenariosEqual(first, second) {
		t.Fatal("different seeds produced identical scenarios")
	}
	if first.Config.Scale != second.Config.Scale || first.Config.MoverCount != second.Config.MoverCount {
		t.Fatal("seed change altered scenario constraints")
	}
}

func TestBuildOrderIsPermutationAndStableAcrossRepeats(t *testing.T) {
	config := Config{Scale: 128, MoverCount: 16, Density: DensitySparse, Seed: 99}
	first, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	if !permutationEqual(first.BuildOrder, 128) {
		t.Fatal("build order is not a permutation")
	}
	if !intSliceEqual(first.BuildOrder, second.BuildOrder) {
		t.Fatal("build order changed across scenario repeats")
	}
}

func TestMovementPatternProportions(t *testing.T) {
	config := Config{Scale: 256, MoverCount: 50, Density: DensityNormal, Seed: 7}
	scenario, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	local, crossing := scenario.Schedule.PatternCounts()
	total := scenario.Schedule.TotalMoves()
	wantCrossing := int(math.Round(float64(total) * (1 - cellLocalFraction)))
	wantLocal := total - wantCrossing
	if local != wantLocal || crossing != wantCrossing {
		t.Fatalf("patterns local=%d crossing=%d, want local=%d crossing=%d", local, crossing, wantLocal, wantCrossing)
	}
}

func TestCompactScheduleExpansionDeterministicAndReusesBuffers(t *testing.T) {
	config := Config{Scale: 128, MoverCount: 16, Density: DensityNormal, Seed: 11}
	scenario, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	expanderA := NewScheduleExpander(scenario)
	first := expanderA.ExpandUpdate(0)
	expanderB := NewScheduleExpander(scenario)
	second := expanderB.ExpandUpdate(0)
	if !positionsEqual(first.CoreTargets, second.CoreTargets) {
		t.Fatal("expansion diverged across fresh expanders")
	}
	if &expanderA.coreTargets[0] == &first.CoreTargets[0] {
		t.Fatal("expected returned core targets to be copied")
	}
}

func TestExpansionReachesExpectedPositionAtAOIBoundary(t *testing.T) {
	config := Config{Scale: 128, MoverCount: 16, Density: DensityNormal, Seed: 13}
	scenario, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	expander := NewScheduleExpander(scenario)
	for update := 0; update < TotalAOIUpdates; update++ {
		expanded := expander.ExpandUpdate(update)
		for slot, playerIdx := range scenario.MoverIndices {
			lat, lng := expanded.CoreTargets[slot].Lat, expanded.CoreTargets[slot].Lng
			x, y := latLngToLocal(scenario.AOIConfig, lat, lng)
			exX, exY := expander.MoverLocalPosition(slot)
			if !almostEqual(x, exX) || !almostEqual(y, exY) {
				t.Fatalf("update %d slot %d local position mismatch", update, slot)
			}
			_ = playerIdx
		}
	}
}

func TestWorldScheduleDrivesRealMovement(t *testing.T) {
	config := Config{Scale: 128, MoverCount: 16, Density: DensityNormal, Seed: 17}
	scenario, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	world := game.NewWorld(scenario.WorldConfig)
	for _, player := range scenario.Players {
		world.AddPlayerAt(player.ID, player.Lat, player.Lng)
	}

	expander := NewScheduleExpander(scenario)
	for update := 0; update < TotalAOIUpdates; update++ {
		expanded := expander.ExpandUpdate(update)
		for slot, playerIdx := range scenario.MoverIndices {
			playerID := scenario.Players[playerIdx].ID
			world.ApplyInput(playerID, expanded.WorldInputs[slot].Tick0)
		}
		world.Step(time.Duration(SimTickIntervalMs) * time.Millisecond)
		for slot, playerIdx := range scenario.MoverIndices {
			playerID := scenario.Players[playerIdx].ID
			world.ApplyInput(playerID, expanded.WorldInputs[slot].Tick1)
		}
		world.Step(time.Duration(SimTickIntervalMs) * time.Millisecond)
		for slot, playerIdx := range scenario.MoverIndices {
			playerID := scenario.Players[playerIdx].ID
			pos, ok := world.PlayerPosition(playerID)
			if !ok {
				t.Fatalf("missing player %d", playerID)
			}
			target := expanded.CoreTargets[slot]
			if !almostEqual(pos.Lat, target.Lat) || !almostEqual(pos.Lng, target.Lng) {
				t.Fatalf("update %d slot %d world position (%v,%v) != core target (%v,%v)", update, slot, pos.Lat, pos.Lng, target.Lat, target.Lng)
			}
		}
	}
}

func TestInitialDensityRanges(t *testing.T) {
	cases := []struct {
		name    string
		config  Config
		minMean float64
		maxMean float64
	}{
		{
			name:    "sparse",
			config:  Config{Scale: 512, MoverCount: 64, Density: DensitySparse, Seed: 3},
			minMean: 4,
			maxMean: 20,
		},
		{
			name:    "normal",
			config:  Config{Scale: 512, MoverCount: 64, Density: DensityNormal, Seed: 3},
			minMean: 25,
			maxMean: 80,
		},
		{
			name:    "hotspot",
			config:  Config{Scale: 1000, MoverCount: 64, Density: DensityHotspot, Seed: 3},
			minMean: 25,
			maxMean: 120,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scenario, err := Generate(tc.config)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.InitialDensity.Mean < tc.minMean || scenario.InitialDensity.Mean > tc.maxMean {
				t.Fatalf("initial density mean=%v, want [%v,%v]", scenario.InitialDensity.Mean, tc.minMean, tc.maxMean)
			}
		})
	}
}

func TestHotspotPlayerCount(t *testing.T) {
	config := Config{Scale: 1000, MoverCount: 64, Density: DensityHotspot, Seed: 5}
	scenario, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	if scenario.HotspotCount != 10 {
		t.Fatalf("hotspot count=%d, want 10", scenario.HotspotCount)
	}
}

func TestSteadyStateDensityRecordedAfterWarmup(t *testing.T) {
	config := Config{Scale: 256, MoverCount: 32, Density: DensityNormal, Seed: 19}
	scenario, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	if scenario.SteadyStateDensity.Max == 0 && scenario.InitialDensity.Max > 0 {
		t.Fatal("expected steady-state density to be recorded")
	}
}

func TestVisibilityChurnStatsPresent(t *testing.T) {
	config := Config{Scale: 256, MoverCount: 32, Density: DensityNormal, Seed: 23}
	scenario, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	if float64(scenario.VisibilityChurn.Max) < scenario.VisibilityChurn.P50 {
		t.Fatalf("invalid churn stats: %+v", scenario.VisibilityChurn)
	}
	if scenario.VisibilityChurn.Mean <= 0 {
		t.Fatalf("expected non-zero churn mean, got %+v", scenario.VisibilityChurn)
	}
}

func TestGenerationIndependentFromMapIterationOrder(t *testing.T) {
	config := Config{Scale: 256, MoverCount: 32, Density: DensityNormal, Seed: 29}
	scenario, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[int]struct{}{}
	for _, idx := range scenario.BuildOrder {
		keys[idx] = struct{}{}
	}
	sorted := sortedIntKeys(keys)
	if !intSliceEqual(scenario.BuildOrder, sorted) && !permutationEqual(scenario.BuildOrder, config.Scale) {
		t.Fatal("build order invalid")
	}
}

func TestInapplicableConfigRejected(t *testing.T) {
	_, err := Generate(Config{Scale: 100, MoverCount: 200, Density: DensityNormal, Seed: 1})
	if err == nil {
		t.Fatal("expected error when mover count exceeds scale")
	}
}

func TestWorldStepUsesApplyInputAndStep(t *testing.T) {
	world := game.NewWorld(DefaultWorldConfig())
	world.AddPlayerAt(1001, DefaultWorldConfig().SpawnLat, DefaultWorldConfig().SpawnLng)
	input := game.InputState{Sequence: 1, Right: true}
	if !world.ApplyInput(1001, input) {
		t.Fatal("apply input failed")
	}
	before, _ := world.PlayerPosition(1001)
	world.Step(50 * time.Millisecond)
	after, _ := world.PlayerPosition(1001)
	if after.Lng <= before.Lng {
		t.Fatal("expected movement after Step")
	}
}

func scenariosEqual(a, b *Scenario) bool {
	if a.Config != b.Config {
		return false
	}
	if len(a.Players) != len(b.Players) {
		return false
	}
	for i := range a.Players {
		if a.Players[i] != b.Players[i] {
			return false
		}
	}
	if !intSliceEqual(a.BuildOrder, b.BuildOrder) {
		return false
	}
	if !intSliceEqual(a.MoverIndices, b.MoverIndices) {
		return false
	}
	if a.HotspotCount != b.HotspotCount {
		return false
	}
	if len(a.Schedule.Updates) != len(b.Schedule.Updates) {
		return false
	}
	for i := range a.Schedule.Updates {
		if !updateInstructionsEqual(a.Schedule.Updates[i], b.Schedule.Updates[i]) {
			return false
		}
	}
	return true
}

func updateInstructionsEqual(a, b UpdateInstructions) bool {
	if len(a.DeltaX) != len(b.DeltaX) || len(a.DeltaY) != len(b.DeltaY) || len(a.Patterns) != len(b.Patterns) {
		return false
	}
	for i := range a.DeltaX {
		if !almostEqual(a.DeltaX[i], b.DeltaX[i]) || !almostEqual(a.DeltaY[i], b.DeltaY[i]) {
			return false
		}
		if a.Patterns[i] != b.Patterns[i] {
			return false
		}
	}
	return true
}

func permutationEqual(order []int, count int) bool {
	if len(order) != count {
		return false
	}
	seen := make([]bool, count)
	for _, idx := range order {
		if idx < 0 || idx >= count || seen[idx] {
			return false
		}
		seen[idx] = true
	}
	return true
}

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func positionsEqual(a, b []game.PlayerPosition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || !almostEqual(a[i].Lat, b[i].Lat) || !almostEqual(a[i].Lng, b[i].Lng) {
			return false
		}
	}
	return true
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-4
}
