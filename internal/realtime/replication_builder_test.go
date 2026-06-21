package realtime

import (
	"testing"

	"map-walker/internal/game"
)

// 构建确定性 fixture：两个玩家在上海，相互可见
func setupReaderFixture() (*game.World, *game.AOIIndex, map[int64]ClientSender) {
	cfg := game.DefaultConfig()
	world := game.NewWorld(cfg)
	world.AddPlayerWithState(1, "alice", 31.2304, 121.4737, game.DefaultAppearance())
	world.AddPlayerWithState(2, "bob", 31.2305, 121.4738, game.DefaultAppearance())

	aoi := game.NewAOIIndex(game.AOIConfigFromWorld(cfg))
	aoi.Insert(1, 31.2304, 121.4737)
	aoi.Insert(2, 31.2305, 121.4738)

	clients := map[int64]ClientSender{
		1: NewTestClient(1, 256),
		2: NewTestClient(2, 256),
	}

	return world, aoi, clients
}

func TestHubReaderAndConcreteReaderEquivalent(t *testing.T) {
	world, aoi, clients := setupReaderFixture()

	hr := &hubReader{clients: clients, aoi: aoi, world: world}
	cr := &concreteReader{clients: clients, aoi: aoi, world: world}

	readers := []struct {
		name string
		r    ReplicationBuildReader
	}{
		{"hubReader", hr},
		{"concreteReader", cr},
	}

	for _, tc := range readers {
		t.Run(tc.name, func(t *testing.T) {
			// Connected
			if !tc.r.Connected(1) {
				t.Error("player 1 should be connected")
			}
			if tc.r.Connected(999) {
				t.Error("player 999 should not be connected")
			}

			// Client
			c, ok := tc.r.Client(1)
			if !ok || c.ID() != 1 {
				t.Error("client 1 should exist with correct ID")
			}
			_, ok = tc.r.Client(999)
			if ok {
				t.Error("client 999 should not exist")
			}

			// VisibleNeighbors
			neighbors := tc.r.VisibleNeighbors(1)
			if len(neighbors) == 0 {
				t.Error("player 1 should have visible neighbors")
			}

			// PlayerPosition
			pos, ok := tc.r.PlayerPosition(1)
			if !ok || pos.ID != 1 {
				t.Error("should get position for player 1")
			}
			_, ok = tc.r.PlayerPosition(999)
			if ok {
				t.Error("should not get position for unknown player")
			}
		})
	}
}

func TestReadersReturnSameValues(t *testing.T) {
	world, aoi, clients := setupReaderFixture()

	hr := &hubReader{clients: clients, aoi: aoi, world: world}
	cr := &concreteReader{clients: clients, aoi: aoi, world: world}

	for _, playerID := range []int64{1, 2} {
		// Connected
		if hr.Connected(playerID) != cr.Connected(playerID) {
			t.Errorf("Connected(%d): hubReader=%v, concreteReader=%v",
				playerID, hr.Connected(playerID), cr.Connected(playerID))
		}

		// Client
		hClient, hOk := hr.Client(playerID)
		cClient, cOk := cr.Client(playerID)
		if hOk != cOk || (hOk && hClient.ID() != cClient.ID()) {
			t.Errorf("Client(%d): hubReader=(%v,%v), concreteReader=(%v,%v)",
				playerID, hClient, hOk, cClient, cOk)
		}

		// VisibleNeighbors
		hN := hr.VisibleNeighbors(playerID)
		cN := cr.VisibleNeighbors(playerID)
		if len(hN) != len(cN) {
			t.Errorf("VisibleNeighbors(%d): hubReader=%v, concreteReader=%v",
				playerID, hN, cN)
		}

		// PlayerPosition
		hPos, hOk := hr.PlayerPosition(playerID)
		cPos, cOk := cr.PlayerPosition(playerID)
		if hOk != cOk || (hOk && hPos != cPos) {
			t.Errorf("PlayerPosition(%d): hubReader=(%v,%v), concreteReader=(%v,%v)",
				playerID, hPos, hOk, cPos, cOk)
		}
	}
}

func TestReplicationBuildInputZeroValue(t *testing.T) {
	// 空输入不 panic
	input := ReplicationBuildInput{}
	if input.Tick != 0 {
		t.Error("zero Tick should be 0")
	}
}
