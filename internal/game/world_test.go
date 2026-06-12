package game

import (
	"math"
	"testing"
	"time"
)

func TestWorldAddPlayerAtUsesExplicitPosition(t *testing.T) {
	world := NewWorld(testConfig())

	if added := world.AddPlayerAt("alice", 31.5, 121.5); !added {
		t.Fatal("expected alice to be added")
	}

	snapshot := world.Snapshot()
	if len(snapshot.Players) != 1 {
		t.Fatalf("expected 1 player, got %d", len(snapshot.Players))
	}
	if snapshot.Players[0] != (PlayerState{ID: "alice", Username: "alice", Lat: 31.5, Lng: 121.5, Appearance: DefaultAppearance()}) {
		t.Fatalf("unexpected position: %+v", snapshot.Players[0])
	}
}

func TestWorldAddPlayerUsesConfiguredSpawn(t *testing.T) {
	world := NewWorld(Config{
		SpawnLat:             31.2304,
		SpawnLng:             121.4737,
		SpeedMetersPerSecond: 12,
	})

	if added := world.AddPlayer("alice"); !added {
		t.Fatal("expected alice to be added")
	}

	snapshot := world.Snapshot()
	if snapshot.Tick != 0 {
		t.Fatalf("expected tick 0, got %d", snapshot.Tick)
	}
	if len(snapshot.Players) != 1 {
		t.Fatalf("expected 1 player, got %d", len(snapshot.Players))
	}
	if snapshot.Players[0] != (PlayerState{ID: "alice", Username: "alice", Lat: 31.2304, Lng: 121.4737, Appearance: DefaultAppearance()}) {
		t.Fatalf("unexpected spawn: %+v", snapshot.Players[0])
	}
}

func TestWorldApplyInputRejectsOldSequence(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")

	if accepted := world.ApplyInput("alice", InputState{Sequence: 2, Right: true}); !accepted {
		t.Fatal("expected newer input to be accepted")
	}
	if accepted := world.ApplyInput("alice", InputState{Sequence: 2, Left: true}); accepted {
		t.Fatal("expected duplicate sequence to be rejected")
	}
	if accepted := world.ApplyInput("alice", InputState{Sequence: 1, Left: true}); accepted {
		t.Fatal("expected stale sequence to be rejected")
	}

	world.Step(time.Second)
	position := world.Snapshot().Players[0]
	if position.Lng <= 121.4737 {
		t.Fatalf("expected accepted right input to win, got %+v", position)
	}
}

func TestWorldMovementDependsOnDurationNotInputFrequency(t *testing.T) {
	oneMessage := newTestWorld()
	oneMessage.AddPlayer("alice")
	oneMessage.ApplyInput("alice", InputState{Sequence: 1, Right: true})
	for range 20 {
		oneMessage.Step(50 * time.Millisecond)
	}

	manyMessages := newTestWorld()
	manyMessages.AddPlayer("alice")
	for sequence := uint64(1); sequence <= 20; sequence++ {
		manyMessages.ApplyInput("alice", InputState{Sequence: sequence, Right: true})
		manyMessages.Step(50 * time.Millisecond)
	}

	one := oneMessage.Snapshot().Players[0]
	many := manyMessages.Snapshot().Players[0]
	if !almostEqual(one.Lat, many.Lat) || !almostEqual(one.Lng, many.Lng) {
		t.Fatalf("input frequency changed movement: one=%+v many=%+v", one, many)
	}
}

func TestWorldDiagonalSpeedMatchesStraightSpeed(t *testing.T) {
	straight := newTestWorld()
	straight.AddPlayer("alice")
	straight.ApplyInput("alice", InputState{Sequence: 1, Right: true})
	straight.Step(time.Second)

	diagonal := newTestWorld()
	diagonal.AddPlayer("alice")
	diagonal.ApplyInput("alice", InputState{Sequence: 1, Up: true, Right: true})
	diagonal.Step(time.Second)

	straightDistance := distanceMeters(testConfig().SpawnLat, testConfig().SpawnLng, straight.Snapshot().Players[0])
	diagonalDistance := distanceMeters(testConfig().SpawnLat, testConfig().SpawnLng, diagonal.Snapshot().Players[0])
	if math.Abs(straightDistance-diagonalDistance) > 0.01 {
		t.Fatalf("expected equal speeds: straight=%f diagonal=%f", straightDistance, diagonalDistance)
	}
}

func TestWorldOppositeDirectionsCancel(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")
	world.TakeDelta()
	world.ApplyInput("alice", InputState{
		Sequence: 1,
		Up:       true,
		Down:     true,
		Left:     true,
		Right:    true,
	})

	world.Step(time.Second)

	if delta := world.TakeDelta(); delta.HasChanges() {
		t.Fatalf("expected no movement delta, got %+v", delta)
	}
}

func TestWorldMergesSeveralStepsIntoLatestDelta(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")
	world.TakeDelta()
	world.ApplyInput("alice", InputState{Sequence: 1, Up: true})

	world.Step(50 * time.Millisecond)
	world.Step(50 * time.Millisecond)

	delta := world.TakeDelta()
	if delta.Tick != 2 {
		t.Fatalf("expected tick 2, got %d", delta.Tick)
	}
	if len(delta.Players) != 1 || delta.Players[0].ID != "alice" {
		t.Fatalf("expected one latest alice position, got %+v", delta.Players)
	}
	if next := world.TakeDelta(); next.HasChanges() {
		t.Fatalf("expected TakeDelta to clear changes, got %+v", next)
	}
}

func TestWorldRemovePlayerReportsOnlyRemoval(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")
	world.RemovePlayer("alice")

	delta := world.TakeDelta()
	if len(delta.Players) != 0 {
		t.Fatalf("removed player must not remain dirty: %+v", delta.Players)
	}
	if len(delta.RemovedPlayerIDs) != 1 || delta.RemovedPlayerIDs[0] != "alice" {
		t.Fatalf("unexpected removals: %+v", delta.RemovedPlayerIDs)
	}
}

func TestWorldAddPlayerWithAppearance(t *testing.T) {
	world := NewWorld(testConfig())

	custom := Appearance{Color: "#ff6600", Shape: ShapeDiamond}
	if added := world.AddPlayerWithAppearance("alice", 31.5, 121.5, custom); !added {
		t.Fatal("expected alice to be added")
	}

	appearance, ok := world.PlayerAppearance("alice")
	if !ok || appearance != custom {
		t.Fatalf("unexpected appearance: ok=%v appearance=%+v", ok, appearance)
	}

	snapshot := world.Snapshot()
	if snapshot.Players[0].Appearance != custom {
		t.Fatalf("snapshot appearance = %+v, want %+v", snapshot.Players[0].Appearance, custom)
	}
}

func TestWorldUpdatePlayerAppearance(t *testing.T) {
	world := NewWorld(testConfig())
	world.AddPlayer("alice")

	updated := Appearance{Color: "#ff6600", Shape: ShapeTriangle}
	changed, ok := world.UpdatePlayerAppearance("alice", updated)
	if !ok || !changed {
		t.Fatalf("expected changed appearance update, changed=%v ok=%v", changed, ok)
	}

	appearance, ok := world.PlayerAppearance("alice")
	if !ok || appearance != updated {
		t.Fatalf("unexpected appearance: %+v", appearance)
	}

	changed, ok = world.UpdatePlayerAppearance("alice", updated)
	if !ok || changed {
		t.Fatalf("expected unchanged appearance update, changed=%v ok=%v", changed, ok)
	}

	changed, ok = world.UpdatePlayerAppearance("missing", updated)
	if ok || changed {
		t.Fatalf("expected missing player update to fail, changed=%v ok=%v", changed, ok)
	}
}

func TestWorldMovementPreservesAppearance(t *testing.T) {
	world := NewWorld(testConfig())

	custom := Appearance{Color: "#ff6600", Shape: ShapeSquare}
	world.AddPlayerWithAppearance("alice", testConfig().SpawnLat, testConfig().SpawnLng, custom)
	world.TakeDelta()
	world.ApplyInput("alice", InputState{Sequence: 1, Right: true})
	world.Step(time.Second)

	position, ok := world.PlayerPosition("alice")
	if !ok {
		t.Fatal("expected alice position")
	}
	if position.Lng <= testConfig().SpawnLng {
		t.Fatalf("expected movement, got %+v", position)
	}

	appearance, ok := world.PlayerAppearance("alice")
	if !ok || appearance != custom {
		t.Fatalf("appearance after movement = %+v, want %+v", appearance, custom)
	}

	delta := world.TakeDelta()
	if len(delta.Players) != 1 {
		t.Fatalf("expected one delta player, got %+v", delta.Players)
	}
	if delta.Players[0].ID != position.ID ||
		delta.Players[0].Lat != position.Lat ||
		delta.Players[0].Lng != position.Lng {
		t.Fatalf("delta position = %+v, want %+v", delta.Players[0], position)
	}
}

func TestWorldResetInputAllowsReplacementSequenceToRestart(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")
	world.ApplyInput("alice", InputState{Sequence: 100, Right: true})

	world.ResetInput("alice")

	if accepted := world.ApplyInput("alice", InputState{Sequence: 1, Left: true}); !accepted {
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
