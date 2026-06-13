package aoiworkload

import (
	"math"
	"time"

	"map-walker/internal/game"
)

var worldDirectionInputs = []struct {
	up, down, left, right bool
}{
	{},
	{up: true},
	{down: true},
	{left: true},
	{right: true},
	{up: true, right: true},
	{up: true, left: true},
	{down: true, right: true},
	{down: true, left: true},
}

func worldInputsForTarget(
	worldConfig game.Config,
	aoiConfig game.AOIConfig,
	startLat, startLng float64,
	targetLat, targetLng float64,
	baseSequence uint64,
) MoverWorldInputs {
	best := MoverWorldInputs{
		Tick0: game.InputState{Sequence: baseSequence + 1},
		Tick1: game.InputState{Sequence: baseSequence + 2},
	}
	bestDistance := math.MaxFloat64

	for _, dir0 := range worldDirectionInputs {
		tick0 := game.InputState{
			Sequence: baseSequence + 1,
			Up:       dir0.up,
			Down:     dir0.down,
			Left:     dir0.left,
			Right:    dir0.right,
		}
		midLat, midLng := worldStepOnce(worldConfig, startLat, startLng, tick0)
		for _, dir1 := range worldDirectionInputs {
			tick1 := game.InputState{
				Sequence: baseSequence + 2,
				Up:       dir1.up,
				Down:     dir1.down,
				Left:     dir1.left,
				Right:    dir1.right,
			}
			endLat, endLng := worldStepOnce(worldConfig, midLat, midLng, tick1)
			distance := latLngDistanceMeters(aoiConfig, endLat, endLng, targetLat, targetLng)
			if distance < bestDistance {
				bestDistance = distance
				best = MoverWorldInputs{Tick0: tick0, Tick1: tick1}
			}
		}
	}
	return best
}

func latLngDistanceMeters(aoiConfig game.AOIConfig, latA, lngA, latB, lngB float64) float64 {
	xA, yA := latLngToLocal(aoiConfig, latA, lngA)
	xB, yB := latLngToLocal(aoiConfig, latB, lngB)
	dx := xA - xB
	dy := yA - yB
	return math.Hypot(dx, dy)
}

func worldStepOnce(worldConfig game.Config, startLat, startLng float64, input game.InputState) (lat, lng float64) {
	world := game.NewWorld(worldConfig)
	playerID := "world-step-once"
	world.AddPlayerAt(playerID, startLat, startLng)
	world.ApplyInput(playerID, input)
	world.Step(time.Duration(SimTickIntervalMs) * time.Millisecond)
	pos, _ := world.PlayerPosition(playerID)
	return pos.Lat, pos.Lng
}

func boolNumber(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func SimulateMoverWorldStep(
	world *game.World,
	playerID string,
	input game.InputState,
) {
	world.ApplyInput(playerID, input)
	world.Step(time.Duration(SimTickIntervalMs) * time.Millisecond)
}

func WorldPositionAfterInputs(
	worldConfig game.Config,
	aoiConfig game.AOIConfig,
	startLat, startLng float64,
	inputs MoverWorldInputs,
) (lat, lng float64, localX, localY float64) {
	world := game.NewWorld(worldConfig)
	playerID := "world-sim-player"
	world.AddPlayerAt(playerID, startLat, startLng)

	SimulateMoverWorldStep(world, playerID, inputs.Tick0)
	SimulateMoverWorldStep(world, playerID, inputs.Tick1)

	pos, _ := world.PlayerPosition(playerID)
	localX, localY = latLngToLocal(aoiConfig, pos.Lat, pos.Lng)
	return pos.Lat, pos.Lng, localX, localY
}
