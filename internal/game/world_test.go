package game

import (
	"math"
	"testing"
	"time"
)

const (
	idAlice = int64(1001)
	idBob   = int64(1002)
)

func TestWorldAddPlayerAtUsesExplicitPosition(t *testing.T) {
	world := NewWorld(testConfig())

	if added := world.AddPlayerAt(idAlice, 31.5, 121.5); !added {
		t.Fatal("expected alice to be added")
	}

	state, ok := world.PlayerState(idAlice)
	if !ok {
		t.Fatal("expected alice state")
	}
	if state != (PlayerState{ID: idAlice, Username: "", Lat: 31.5, Lng: 121.5, Appearance: DefaultAppearance()}) {
		t.Fatalf("unexpected position: %+v", state)
	}
}

func TestWorldAddPlayerUsesConfiguredSpawn(t *testing.T) {
	world := NewWorld(Config{
		SpawnLat:             31.2304,
		SpawnLng:             121.4737,
		SpeedMetersPerSecond: 12,
	})

	if added := world.AddPlayer(idAlice); !added {
		t.Fatal("expected alice to be added")
	}

	if world.Tick() != 0 {
		t.Fatalf("expected tick 0, got %d", world.Tick())
	}
	state, ok := world.PlayerState(idAlice)
	if !ok {
		t.Fatal("expected alice state")
	}
	if state != (PlayerState{ID: idAlice, Username: "", Lat: 31.2304, Lng: 121.4737, Appearance: DefaultAppearance()}) {
		t.Fatalf("unexpected spawn: %+v", state)
	}
}

func TestWorldApplyInputRejectsOldSequence(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer(idAlice)

	if accepted := world.ApplyInput(idAlice, InputState{Sequence: 2, Right: true}); !accepted {
		t.Fatal("expected newer input to be accepted")
	}
	if accepted := world.ApplyInput(idAlice, InputState{Sequence: 2, Left: true}); accepted {
		t.Fatal("expected duplicate sequence to be rejected")
	}
	if accepted := world.ApplyInput(idAlice, InputState{Sequence: 1, Left: true}); accepted {
		t.Fatal("expected stale sequence to be rejected")
	}

	world.Step(time.Second)
	state, ok := world.PlayerState(idAlice)
	if !ok || state.Lng <= 121.4737 {
		t.Fatalf("expected accepted right input to win, got %+v", state)
	}
}

func TestWorldMovementDependsOnDurationNotInputFrequency(t *testing.T) {
	oneMessage := newTestWorld()
	oneMessage.AddPlayer(idAlice)
	oneMessage.ApplyInput(idAlice, InputState{Sequence: 1, Right: true})
	for range 20 {
		oneMessage.Step(50 * time.Millisecond)
	}

	manyMessages := newTestWorld()
	manyMessages.AddPlayer(idAlice)
	for sequence := uint64(1); sequence <= 20; sequence++ {
		manyMessages.ApplyInput(idAlice, InputState{Sequence: sequence, Right: true})
		manyMessages.Step(50 * time.Millisecond)
	}

	one, _ := oneMessage.PlayerState(idAlice)
	many, _ := manyMessages.PlayerState(idAlice)
	if !almostEqual(one.Lat, many.Lat) || !almostEqual(one.Lng, many.Lng) {
		t.Fatalf("input frequency changed movement: one=%+v many=%+v", one, many)
	}
}

func TestWorldDiagonalSpeedMatchesStraightSpeed(t *testing.T) {
	straight := newTestWorld()
	straight.AddPlayer(idAlice)
	straight.ApplyInput(idAlice, InputState{Sequence: 1, Right: true})
	straight.Step(time.Second)

	diagonal := newTestWorld()
	diagonal.AddPlayer(idAlice)
	diagonal.ApplyInput(idAlice, InputState{Sequence: 1, Up: true, Right: true})
	diagonal.Step(time.Second)

	straightState, _ := straight.PlayerState(idAlice)
	diagonalState, _ := diagonal.PlayerState(idAlice)
	straightDistance := distanceMeters(testConfig().SpawnLat, testConfig().SpawnLng, straightState)
	diagonalDistance := distanceMeters(testConfig().SpawnLat, testConfig().SpawnLng, diagonalState)
	if math.Abs(straightDistance-diagonalDistance) > 0.01 {
		t.Fatalf("expected equal speeds: straight=%f diagonal=%f", straightDistance, diagonalDistance)
	}
}

func TestWorldOppositeDirectionsCancel(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer(idAlice)
	world.ApplyInput(idAlice, InputState{
		Sequence: 1,
		Up:       true,
		Down:     true,
		Left:     true,
		Right:    true,
	})

	world.Step(time.Second)

	if moved := world.TakeMovedPlayerIDs(); len(moved) != 0 {
		t.Fatalf("expected no moved players, got %+v", moved)
	}
}

func TestWorldAccumulatesMovedPlayersAcrossSimulationTicks(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer(idAlice)
	world.AddPlayer(idBob)
	world.ApplyInput(idAlice, InputState{Sequence: 1, Up: true})
	world.ApplyInput(idBob, InputState{Sequence: 1, Right: true})

	world.Step(50 * time.Millisecond)
	world.Step(50 * time.Millisecond)

	moved := world.TakeMovedPlayerIDs()
	if !slicesEqualInt64(moved, []int64{idAlice, idBob}) {
		t.Fatalf("moved = %+v, want [%d %d]", moved, idAlice, idBob)
	}
	if next := world.TakeMovedPlayerIDs(); len(next) != 0 {
		t.Fatalf("expected one-time consumption, got %+v", next)
	}
}

func TestWorldMergesSeveralStepsIntoLatestReplicationState(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer(idAlice)
	world.ApplyInput(idAlice, InputState{Sequence: 1, Up: true})

	world.Step(50 * time.Millisecond)
	world.Step(50 * time.Millisecond)

	moved := world.TakeMovedPlayerIDs()
	if !slicesEqualInt64(moved, []int64{idAlice}) {
		t.Fatalf("expected one moved alice, got %+v", moved)
	}
	if world.Tick() != 2 {
		t.Fatalf("expected tick 2, got %d", world.Tick())
	}

	state, ok := world.PlayerState(idAlice)
	if !ok || state.Lat <= testConfig().SpawnLat {
		t.Fatalf("expected latest position after two steps, got %+v", state)
	}
}

func TestWorldStaticPlayersAreNotReportedAsMoved(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer(idAlice)
	world.AddPlayer(idBob)
	world.ApplyInput(idAlice, InputState{Sequence: 1, Up: true})

	world.Step(50 * time.Millisecond)

	moved := world.TakeMovedPlayerIDs()
	if !slicesEqualInt64(moved, []int64{idAlice}) {
		t.Fatalf("moved = %+v, want [%d]", moved, idAlice)
	}
}

func TestWorldRemovePlayerReportsOnlyRemoval(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer(idAlice)
	world.RemovePlayer(idAlice)

	if moved := world.TakeMovedPlayerIDs(); len(moved) != 0 {
		t.Fatalf("removed player must not remain moved: %+v", moved)
	}
	removed := world.TakeRemovedPlayerIDs()
	if !slicesEqualInt64(removed, []int64{idAlice}) {
		t.Fatalf("unexpected removals: %+v", removed)
	}
}

func TestWorldPlayerStateAndPositionLookups(t *testing.T) {
	world := NewWorld(testConfig())
	custom := Appearance{Color: "#ff6600", Shape: ShapeDiamond}
	world.AddPlayerWithState(idAlice, "Alice", 31.5, 121.5, custom)

	state, ok := world.PlayerState(idAlice)
	if !ok || state.Username != "Alice" || state.Appearance != custom {
		t.Fatalf("unexpected complete state: ok=%v state=%+v", ok, state)
	}

	position, ok := world.PlayerPosition(idAlice)
	if !ok || position != (PlayerPosition{ID: idAlice, Lat: 31.5, Lng: 121.5}) {
		t.Fatalf("unexpected position-only state: %+v", position)
	}

	if _, ok := world.PlayerState(9999); ok {
		t.Fatal("expected missing player lookup to fail")
	}
	if _, ok := world.PlayerPosition(9999); ok {
		t.Fatal("expected missing position lookup to fail")
	}
}

func TestWorldPlayerStatesAndPositionsFilterMissingPlayers(t *testing.T) {
	world := newTestWorld()
	world.AddPlayerWithState(idAlice, "Alice", 31.1, 121.1, DefaultAppearance())
	world.AddPlayerWithState(idBob, "Bob", 31.2, 121.2, DefaultAppearance())

	states := world.PlayerStates([]int64{idBob, 9999, idAlice})
	if len(states) != 2 || states[0].ID != idBob || states[1].ID != idAlice {
		t.Fatalf("unexpected states: %+v", states)
	}

	positions := world.PlayerPositions([]int64{idAlice, 9999})
	if len(positions) != 1 || positions[0].ID != idAlice {
		t.Fatalf("unexpected positions: %+v", positions)
	}
}

func TestWorldAddPlayerWithAppearance(t *testing.T) {
	world := NewWorld(testConfig())

	custom := Appearance{Color: "#ff6600", Shape: ShapeDiamond}
	if added := world.AddPlayerWithAppearance(idAlice, 31.5, 121.5, custom); !added {
		t.Fatal("expected alice to be added")
	}

	appearance, ok := world.PlayerAppearance(idAlice)
	if !ok || appearance != custom {
		t.Fatalf("unexpected appearance: ok=%v appearance=%+v", ok, appearance)
	}

	state, ok := world.PlayerState(idAlice)
	if !ok || state.Appearance != custom {
		t.Fatalf("player state appearance = %+v, want %+v", state.Appearance, custom)
	}
}

func TestWorldUpdatePlayerAppearance(t *testing.T) {
	world := NewWorld(testConfig())
	world.AddPlayer(idAlice)

	updated := Appearance{Color: "#ff6600", Shape: ShapeTriangle}
	changed, ok := world.UpdatePlayerAppearance(idAlice, updated)
	if !ok || !changed {
		t.Fatalf("expected changed appearance update, changed=%v ok=%v", changed, ok)
	}

	appearance, ok := world.PlayerAppearance(idAlice)
	if !ok || appearance != updated {
		t.Fatalf("unexpected appearance: %+v", appearance)
	}

	changed, ok = world.UpdatePlayerAppearance(idAlice, updated)
	if !ok || changed {
		t.Fatalf("expected unchanged appearance update, changed=%v ok=%v", changed, ok)
	}

	changed, ok = world.UpdatePlayerAppearance(9999, updated)
	if ok || changed {
		t.Fatalf("expected missing player update to fail, changed=%v ok=%v", changed, ok)
	}
}

func TestWorldMovementPreservesUsernameAndAppearance(t *testing.T) {
	world := NewWorld(testConfig())

	custom := Appearance{Color: "#ff6600", Shape: ShapeSquare}
	world.AddPlayerWithState(idAlice, "Alice", testConfig().SpawnLat, testConfig().SpawnLng, custom)
	world.ApplyInput(idAlice, InputState{Sequence: 1, Right: true})
	world.Step(time.Second)

	state, ok := world.PlayerState(idAlice)
	if !ok {
		t.Fatal("expected alice state")
	}
	if state.Username != "Alice" {
		t.Fatalf("username after movement = %q, want Alice", state.Username)
	}
	if state.Appearance != custom {
		t.Fatalf("appearance after movement = %+v, want %+v", state.Appearance, custom)
	}
	if state.Lng <= testConfig().SpawnLng {
		t.Fatalf("expected movement, got %+v", state)
	}

	position, ok := world.PlayerPosition(idAlice)
	if !ok || position.Lat != state.Lat || position.Lng != state.Lng {
		t.Fatalf("position = %+v, want lat/lng from %+v", position, state)
	}
}

func TestWorldRemovedPlayersAreUnavailable(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer(idAlice)
	world.RemovePlayer(idAlice)

	if world.HasPlayer(idAlice) {
		t.Fatal("expected alice removed")
	}
	if _, ok := world.PlayerState(idAlice); ok {
		t.Fatal("expected removed player state lookup to fail")
	}
	if _, ok := world.PlayerPosition(idAlice); ok {
		t.Fatal("expected removed player position lookup to fail")
	}
}

func TestWorldResetInputAllowsReplacementSequenceToRestart(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer(idAlice)
	world.ApplyInput(idAlice, InputState{Sequence: 100, Right: true})

	world.ResetInput(idAlice)

	if accepted := world.ApplyInput(idAlice, InputState{Sequence: 1, Left: true}); !accepted {
		t.Fatal("expected replacement connection sequence to restart")
	}
}

func newTestWorld() *World {
	return NewWorld(testConfig())
}

func testConfig() Config {
	return Config{
		SpawnLat:             31.2304,
		SpawnLng:             121.4737,
		SpeedMetersPerSecond: 12,
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}

func distanceMeters(startLat, startLng float64, end PlayerState) float64 {
	latMeters := (end.Lat - startLat) * metersPerDegreeLatitude
	lngMeters := (end.Lng - startLng) * metersPerDegreeLongitude(startLat)
	return math.Hypot(latMeters, lngMeters)
}

func slicesEqualInt64(a, b []int64) bool {
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
