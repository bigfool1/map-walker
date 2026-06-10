package game

import "testing"

func TestStateUpdateAndSnapshot(t *testing.T) {
	state := NewState()

	state.UpdatePosition(PlayerPosition{ID: "alice", Lat: 31.2304, Lng: 121.4737})
	state.UpdatePosition(PlayerPosition{ID: "bob", Lat: 31.2310, Lng: 121.4740})

	snapshot := state.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 players, got %d", len(snapshot))
	}

	players := map[string]PlayerPosition{}
	for _, player := range snapshot {
		players[player.ID] = player
	}

	if players["alice"].Lat != 31.2304 || players["alice"].Lng != 121.4737 {
		t.Fatalf("alice position was not preserved: %+v", players["alice"])
	}
	if players["bob"].Lat != 31.2310 || players["bob"].Lng != 121.4740 {
		t.Fatalf("bob position was not preserved: %+v", players["bob"])
	}
}

func TestStateUpdateReplacesExistingPlayer(t *testing.T) {
	state := NewState()

	state.UpdatePosition(PlayerPosition{ID: "alice", Lat: 1, Lng: 2})
	state.UpdatePosition(PlayerPosition{ID: "alice", Lat: 3, Lng: 4})

	snapshot := state.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 player, got %d", len(snapshot))
	}
	if snapshot[0].Lat != 3 || snapshot[0].Lng != 4 {
		t.Fatalf("expected updated position, got %+v", snapshot[0])
	}
}

func TestStateRemove(t *testing.T) {
	state := NewState()

	state.UpdatePosition(PlayerPosition{ID: "alice", Lat: 1, Lng: 2})
	state.RemovePlayer("alice")

	snapshot := state.Snapshot()
	if len(snapshot) != 0 {
		t.Fatalf("expected no players, got %+v", snapshot)
	}
}
