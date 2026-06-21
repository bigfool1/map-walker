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

// playerFanoutFixture 准备 3 个玩家：alice(1) 和 bob(2) 相互可见，carol(3) 单独
func playerFanoutFixture() (*game.World, *game.AOIIndex, map[int64]ClientSender) {
	cfg := game.DefaultConfig()
	world := game.NewWorld(cfg)
	world.AddPlayerWithState(1, "alice", 31.2304, 121.4737, game.DefaultAppearance())
	world.AddPlayerWithState(2, "bob", 31.2305, 121.4738, game.DefaultAppearance())
	world.AddPlayerWithState(3, "carol", 31.2400, 121.4800, game.DefaultAppearance())

	aoi := game.NewAOIIndex(game.AOIConfigFromWorld(cfg))
	aoi.Insert(1, 31.2304, 121.4737)
	aoi.Insert(2, 31.2305, 121.4738)
	aoi.Insert(3, 31.2400, 121.4800)

	clients := map[int64]ClientSender{
		1: NewTestClient(1, 256),
		2: NewTestClient(2, 256),
		3: NewTestClient(3, 256),
	}
	return world, aoi, clients
}

func TestBuildSelfPositionFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		MovedIDs: []int64{1, 2},
	}
	result := builder.Build(input, reader)

	// alice 和 bob 都移动了，各自应有 SelfPosition
	if result[1] == nil || result[1].SelfPosition == nil {
		t.Fatal("alice should have SelfPosition")
	}
	if result[1].SelfPosition.Lat != 31.2304 || result[1].SelfPosition.Lng != 121.4737 {
		t.Errorf("alice position mismatch: %+v", result[1].SelfPosition)
	}
	if result[2] == nil || result[2].SelfPosition == nil {
		t.Fatal("bob should have SelfPosition")
	}
	// carol 没有移动，不应有条目
	if result[3] != nil {
		t.Error("carol should have no changes")
	}
}

func TestBuildStableNeighborPositionFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	// alice 移动，bob 在移动前后都是 alice 的邻居
	input := ReplicationBuildInput{
		MovedIDs: []int64{1},
		OldNeighborsByMover: map[int64]map[int64]struct{}{
			1: {2: {}},
		},
	}
	result := builder.Build(input, reader)

	// bob 应该收到 alice 的新位置
	if result[2] == nil {
		t.Fatal("bob should have changes")
	}
	if len(result[2].Positions) != 1 {
		t.Fatalf("bob should have 1 position, got %d", len(result[2].Positions))
	}
	if result[2].Positions[0].ID != 1 {
		t.Errorf("position should be alice(1), got %d", result[2].Positions[0].ID)
	}
}

func TestBuildStableNeighborSkipsNotConnected(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	// bob 断连
	delete(clients, 2)
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		MovedIDs: []int64{1},
		OldNeighborsByMover: map[int64]map[int64]struct{}{
			1: {2: {}},
		},
	}
	result := builder.Build(input, reader)

	// bob 断连，不应收到位置
	if result[2] != nil {
		t.Error("disconnected bob should have no changes")
	}
}

func TestBuildEnteredPlayerFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	// bob(2) 对 alice(1) 相互可见
	state := game.PlayerState{ID: 2, Username: "bob", Lat: 31.2305, Lng: 121.4738}
	input := ReplicationBuildInput{
		PendingEntered: []game.PlayerState{state},
	}
	result := builder.Build(input, reader)

	// alice(1) 是 bob 的可见邻居，应收到 entered
	if result[1] == nil {
		t.Fatal("alice should receive bob's entered")
	}
	if len(result[1].Entered) != 1 {
		t.Fatalf("alice should have 1 entered, got %d", len(result[1].Entered))
	}
	if result[1].Entered[0].ID != 2 {
		t.Errorf("entered player should be bob(2), got %d", result[1].Entered[0].ID)
	}
}

func TestBuildLeftPlayerFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		PendingLeft: map[int64][]int64{
			1: {2, 3}, // alice 失去了 bob 和 carol
		},
	}
	result := builder.Build(input, reader)

	if result[1] == nil {
		t.Fatal("alice should have changes")
	}
	if len(result[1].LeftPlayerIDs) != 2 {
		t.Fatalf("alice should have 2 left IDs, got %d", len(result[1].LeftPlayerIDs))
	}
}

func TestBuildAppearanceFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	appearance := game.Appearance{Color: "#ff0000", Shape: "square"}
	input := ReplicationBuildInput{
		PendingAppearances: map[int64]game.Appearance{
			1: appearance, // alice 换了外观
		},
	}
	result := builder.Build(input, reader)

	// alice 本人应收到外观变更
	if result[1] == nil {
		t.Fatal("alice should have changes")
	}
	if len(result[1].Appearances) != 1 {
		t.Fatalf("alice should have 1 appearance, got %d", len(result[1].Appearances))
	}
	if result[1].Appearances[0].PlayerID != 1 {
		t.Errorf("appearance should reference alice(1), got %d", result[1].Appearances[0].PlayerID)
	}

	// bob 是 alice 的可见邻居，应收到外观变更
	if result[2] == nil {
		t.Fatal("bob should have changes")
	}
	if len(result[2].Appearances) != 1 {
		t.Fatalf("bob should have 1 appearance, got %d", len(result[2].Appearances))
	}
}

func TestBuildEmptyInputReturnsEmptyMap(t *testing.T) {
	_, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: game.NewWorld(game.DefaultConfig())}
	var builder ReplicationBuilder

	result := builder.Build(ReplicationBuildInput{}, reader)
	if len(result) != 0 {
		t.Errorf("empty input should produce empty map, got %d entries", len(result))
	}
}

func TestBuildCollectibleEnteredFanout(t *testing.T) {
	_, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: game.NewWorld(game.DefaultConfig())}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		CollectEntered: map[int64][]CollectibleEnteredItem{
			1: {{ID: 100, Lat: 31.23, Lng: 121.47}},
		},
	}
	result := builder.Build(input, reader)

	if result[1] == nil {
		t.Fatal("alice should have changes")
	}
	if len(result[1].CollectiblesEntered) != 1 {
		t.Fatalf("alice should have 1 collectible entered, got %d", len(result[1].CollectiblesEntered))
	}
}

func TestBuildCollectibleLeftFanout(t *testing.T) {
	_, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: game.NewWorld(game.DefaultConfig())}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		CollectLeft: map[int64][]uint64{
			1: {100, 200},
		},
	}
	result := builder.Build(input, reader)

	if result[1] == nil {
		t.Fatal("alice should have changes")
	}
	if len(result[1].CollectibleIDsLeft) != 2 {
		t.Fatalf("alice should have 2 collectibles left, got %d", len(result[1].CollectibleIDsLeft))
	}
}

func TestBuildCollectibleSpawnedFanout(t *testing.T) {
	_, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: game.NewWorld(game.DefaultConfig())}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		CollectSpawned: map[int64][]CollectibleSpawnedItem{
			1: {{ID: 300, Lat: 31.23, Lng: 121.47}},
		},
	}
	result := builder.Build(input, reader)

	if result[1] == nil {
		t.Fatal("alice should have changes")
	}
	if len(result[1].CollectiblesSpawned) != 1 {
		t.Fatalf("alice should have 1 collectible spawned, got %d", len(result[1].CollectiblesSpawned))
	}
}

func TestBuildCollectibleCollectedFanout(t *testing.T) {
	_, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: game.NewWorld(game.DefaultConfig())}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		CollectCollected: map[int64][]uint64{
			1: {100},
		},
	}
	result := builder.Build(input, reader)

	if result[1] == nil {
		t.Fatal("alice should have changes")
	}
	if len(result[1].CollectibleIDsCollected) != 1 {
		t.Fatalf("alice should have 1 collectible collected, got %d", len(result[1].CollectibleIDsCollected))
	}
}

func TestBuildCollectibleSkipsDisconnected(t *testing.T) {
	_, aoi, clients := playerFanoutFixture()
	// bob 断连
	delete(clients, 2)
	reader := &concreteReader{clients: clients, aoi: aoi, world: game.NewWorld(game.DefaultConfig())}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		CollectEntered: map[int64][]CollectibleEnteredItem{
			2: {{ID: 100, Lat: 31.23, Lng: 121.47}},
		},
	}
	result := builder.Build(input, reader)

	if result[2] != nil {
		t.Error("disconnected bob should have no collectible changes")
	}
}
