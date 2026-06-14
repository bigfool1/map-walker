package synthetic

import (
	"math"
	"testing"
	"time"

	"map-walker/internal/game"
)

func TestDirectionIntervalWithinBounds(t *testing.T) {
	for index := 0; index < 20; index++ {
		interval := DirectionInterval(7, 42, index)
		if interval < time.Second || interval > 5*time.Second {
			t.Fatalf("index %d interval = %v, want 1s..5s", index, interval)
		}
	}
}

func TestScheduledDirectionIsDeterministic(t *testing.T) {
	first := ScheduledDirection(12, 99, 3)
	second := ScheduledDirection(12, 99, 3)
	if first != second {
		t.Fatalf("expected stable direction, got %+v and %+v", first, second)
	}
}

func TestScheduledDirectionCoversMovementClasses(t *testing.T) {
	seenNeutral := false
	seenCardinal := false
	seenDiagonal := false

	for index := 0; index < 200; index++ {
		input := ScheduledDirection(5, 7, index)
		switch movementClass(input) {
		case "neutral":
			seenNeutral = true
		case "cardinal":
			seenCardinal = true
		case "diagonal":
			seenDiagonal = true
		}
	}

	if !seenNeutral || !seenCardinal || !seenDiagonal {
		t.Fatalf("coverage neutral=%v cardinal=%v diagonal=%v", seenNeutral, seenCardinal, seenDiagonal)
	}
}

func TestStaggerIsDeterministicAndDistinct(t *testing.T) {
	firstActivation := ActivationStaggerTicks(1, 42)
	secondActivation := ActivationStaggerTicks(1, 42)
	if firstActivation != secondActivation {
		t.Fatalf("activation stagger changed: %d vs %d", firstActivation, secondActivation)
	}

	if ActivationStaggerTicks(1, 42) == ActivationStaggerTicks(2, 42) {
		t.Fatal("expected different accounts to stagger differently")
	}
}

func TestBehaviorSequenceIncreasesOnlyOnChanges(t *testing.T) {
	cfg := DefaultBehaviorConfig()
	behavior := NewBehavior(1, cfg, cfg.Placement.SpawnLat, cfg.Placement.SpawnLng)

	advanceToFirstInput(t, behavior)

	var lastSequence uint64
	for tick := 0; tick < 200; tick++ {
		input, changed := behavior.OnTick()
		if changed {
			if input.Sequence <= lastSequence {
				t.Fatalf("sequence did not increase: last=%d current=%d", lastSequence, input.Sequence)
			}
			lastSequence = input.Sequence
		} else if lastSequence != 0 && input.Sequence != lastSequence {
			t.Fatalf("unexpected sequence on unchanged tick: got=%d want=%d", input.Sequence, lastSequence)
		}
	}
}

func TestEstimatedStraightMovementMatchesWorld(t *testing.T) {
	compareEstimatedToWorld(t, game.InputState{Sequence: 1, Right: true})
}

func TestEstimatedDiagonalMovementMatchesWorld(t *testing.T) {
	compareEstimatedToWorld(t, game.InputState{Sequence: 1, Up: true, Right: true})
}

func TestApplySoftBoundaryFilterForEachEdge(t *testing.T) {
	cases := []struct {
		name   string
		localX float64
		localY float64
		input  game.InputState
		want   game.InputState
	}{
		{
			name:   "east",
			localX: softBoundaryMeters + 100,
			input:  game.InputState{Right: true, Up: true},
			want:   game.InputState{Up: true},
		},
		{
			name:   "west",
			localX: -softBoundaryMeters - 100,
			input:  game.InputState{Left: true, Down: true},
			want:   game.InputState{Down: true},
		},
		{
			name:   "north",
			localY: softBoundaryMeters + 100,
			input:  game.InputState{Up: true, Right: true},
			want:   game.InputState{Right: true},
		},
		{
			name:   "south",
			localY: -softBoundaryMeters - 100,
			input:  game.InputState{Down: true, Left: true},
			want:   game.InputState{Left: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplySoftBoundaryFilter(tc.input, tc.localX, tc.localY)
			if got != tc.want {
				t.Fatalf("filter = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func compareEstimatedToWorld(t *testing.T, input game.InputState) {
	t.Helper()

	cfg := game.DefaultConfig()
	placement := DefaultPlacementConfig()
	delta := BehaviorTickInterval

	world := game.NewWorld(cfg)
	world.AddPlayerAt(1001, cfg.SpawnLat, cfg.SpawnLng)
	world.ApplyInput(1001, input)
	world.Step(delta)

	state, ok := world.PlayerState(1001)
	if !ok {
		t.Fatal("expected world player state")
	}
	worldLocalX, worldLocalY := LatLngToLocal(placement, state.Lat, state.Lng)

	estimatedX, estimatedY := AdvanceEstimatedLocalPosition(0, 0, input, cfg, delta)
	if math.Abs(estimatedX-worldLocalX) > 0.01 || math.Abs(estimatedY-worldLocalY) > 0.01 {
		t.Fatalf("estimate=(%v,%v) world=(%v,%v)", estimatedX, estimatedY, worldLocalX, worldLocalY)
	}
}

func advanceToFirstInput(t *testing.T, behavior *Behavior) {
	t.Helper()
	for {
		_, changed := behavior.OnTick()
		if changed {
			return
		}
	}
}

func movementClass(input game.InputState) string {
	active := 0
	if input.Up {
		active++
	}
	if input.Down {
		active++
	}
	if input.Left {
		active++
	}
	if input.Right {
		active++
	}
	switch active {
	case 0:
		return "neutral"
	case 1:
		return "cardinal"
	default:
		return "diagonal"
	}
}
