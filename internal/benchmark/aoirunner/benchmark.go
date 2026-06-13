package aoirunner

import (
	"time"

	"map-walker/internal/benchmark/aoiworkload"
	"map-walker/internal/game"
)

const BenchmarkSeed int64 = 42

var (
	SmallBenchmarkConfig = aoiworkload.Config{
		Scale:      128,
		MoverCount: 16,
		Density:    aoiworkload.DensityNormal,
		Seed:       BenchmarkSeed,
	}
	ProfileBenchmarkConfig = aoiworkload.Config{
		Scale:      1_024,
		MoverCount: 64,
		Density:    aoiworkload.DensityNormal,
		Seed:       BenchmarkSeed,
	}
)

func Representative100kBenchmarkConfig() aoiworkload.Config {
	return aoiworkload.BaselineMatrixConfigs(BenchmarkSeed)[0]
}

func mustBenchmarkScenario(config aoiworkload.Config) *aoiworkload.Scenario {
	scenario, err := aoiworkload.Generate(config)
	if err != nil {
		panic(err)
	}
	return scenario
}

func warmupCoreTicks(aoi *game.AOIIndex, targets [][]game.PlayerPosition) {
	for update := 0; update < aoiworkload.WarmupAOIUpdates; update++ {
		applyCoreMoves(aoi, targets[update])
		aoi.TakeStats()
	}
}

func runMeasuredCoreTicks(aoi *game.AOIIndex, targets [][]game.PlayerPosition) {
	for update := aoiworkload.WarmupAOIUpdates; update < aoiworkload.TotalAOIUpdates; update++ {
		applyCoreMoves(aoi, targets[update])
		aoi.TakeStats()
	}
}

func warmupWorldSimulation(
	world *game.World,
	aoi *game.AOIIndex,
	scenario *aoiworkload.Scenario,
	updates []expandedWorldUpdate,
) {
	runWorldSimulationTicks(world, aoi, scenario, updates, 0, WorldWarmupSimTicks)
}

func runMeasuredWorldSimulation(
	world *game.World,
	aoi *game.AOIIndex,
	scenario *aoiworkload.Scenario,
	updates []expandedWorldUpdate,
) {
	runWorldSimulationTicks(world, aoi, scenario, updates, WorldWarmupSimTicks, WorldTotalSimTicks)
}

func runWorldSimulationTicks(
	world *game.World,
	aoi *game.AOIIndex,
	scenario *aoiworkload.Scenario,
	updates []expandedWorldUpdate,
	startTick, endTick int,
) {
	tickInterval := time.Duration(aoiworkload.SimTickIntervalMs) * time.Millisecond
	for simTick := startTick; simTick < endTick; simTick++ {
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
		applyAOIFromWorld(world, aoi)
		aoi.TakeStats()
	}
}

func newWorldBenchmarkState(scenario *aoiworkload.Scenario) (*game.World, *game.AOIIndex, []expandedWorldUpdate) {
	world := game.NewWorld(scenario.WorldConfig)
	for _, player := range scenario.Players {
		world.AddPlayerAt(player.ID, player.Lat, player.Lng)
	}
	return world, buildAOIIndex(scenario), preExpandWorldUpdates(scenario)
}
