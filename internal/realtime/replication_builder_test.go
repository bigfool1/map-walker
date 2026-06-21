package realtime

import (
	"testing"

	"map-walker/internal/game"
)

// findJob 在 jobs 切片中查找指定 recipientID 的 job。
func findJob(jobs []replicationJob, recipientID int64) *replicationJob {
	for i := range jobs {
		if jobs[i].recipientID == recipientID {
			return &jobs[i]
		}
	}
	return nil
}

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

// --- Reader tests ---

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
			if !tc.r.Connected(1) {
				t.Error("player 1 should be connected")
			}
			if tc.r.Connected(999) {
				t.Error("player 999 should not be connected")
			}

			c, ok := tc.r.Client(1)
			if !ok || c.ID() != 1 {
				t.Error("client 1 should exist with correct ID")
			}

			neighbors := tc.r.VisibleNeighbors(1)
			if len(neighbors) == 0 {
				t.Error("player 1 should have visible neighbors")
			}

			pos, ok := tc.r.PlayerPosition(1)
			if !ok || pos.ID != 1 {
				t.Error("should get position for player 1")
			}
		})
	}
}

func TestReadersReturnSameValues(t *testing.T) {
	world, aoi, clients := setupReaderFixture()

	hr := &hubReader{clients: clients, aoi: aoi, world: world}
	cr := &concreteReader{clients: clients, aoi: aoi, world: world}

	for _, playerID := range []int64{1, 2} {
		hN := hr.VisibleNeighbors(playerID)
		cN := cr.VisibleNeighbors(playerID)
		if len(hN) != len(cN) {
			t.Errorf("VisibleNeighbors(%d): hubReader=%v, concreteReader=%v", playerID, hN, cN)
		}

		hPos, hOk := hr.PlayerPosition(playerID)
		cPos, cOk := cr.PlayerPosition(playerID)
		if hOk != cOk || (hOk && hPos != cPos) {
			t.Errorf("PlayerPosition(%d): hubReader=(%v,%v), concreteReader=(%v,%v)",
				playerID, hPos, hOk, cPos, cOk)
		}
	}
}

func TestReplicationBuildInputZeroValue(t *testing.T) {
	input := ReplicationBuildInput{}
	if input.Tick != 0 {
		t.Error("zero Tick should be 0")
	}
}

// --- Player fanout tests ---

func TestBuildSelfPositionFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		MovedIDs: []int64{1, 2},
	}
	jobs := builder.Build(input, reader)

	alice := findJob(jobs, 1)
	if alice == nil || alice.changes.SelfPosition == nil {
		t.Fatal("alice should have SelfPosition")
	}
	if alice.changes.SelfPosition.Lat != 31.2304 || alice.changes.SelfPosition.Lng != 121.4737 {
		t.Errorf("alice position mismatch: %+v", alice.changes.SelfPosition)
	}

	bob := findJob(jobs, 2)
	if bob == nil || bob.changes.SelfPosition == nil {
		t.Fatal("bob should have SelfPosition")
	}

	if findJob(jobs, 3) != nil {
		t.Error("carol should have no job")
	}
}

func TestBuildStableNeighborPositionFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		MovedIDs: []int64{1},
		OldNeighborsByMover: map[int64]map[int64]struct{}{
			1: {2: {}},
		},
	}
	jobs := builder.Build(input, reader)

	bob := findJob(jobs, 2)
	if bob == nil {
		t.Fatal("bob should have changes")
	}
	if len(bob.changes.Positions) != 1 {
		t.Fatalf("bob should have 1 position, got %d", len(bob.changes.Positions))
	}
	if bob.changes.Positions[0].ID != 1 {
		t.Errorf("position should be alice(1), got %d", bob.changes.Positions[0].ID)
	}
}

func TestBuildStableNeighborSkipsNotConnected(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	delete(clients, 2)
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		MovedIDs: []int64{1},
		OldNeighborsByMover: map[int64]map[int64]struct{}{
			1: {2: {}},
		},
	}
	jobs := builder.Build(input, reader)

	if findJob(jobs, 2) != nil {
		t.Error("disconnected bob should have no job")
	}
}

func TestBuildEnteredPlayerFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	state := game.PlayerState{ID: 2, Username: "bob", Lat: 31.2305, Lng: 121.4738}
	input := ReplicationBuildInput{
		PendingEntered: []game.PlayerState{state},
	}
	jobs := builder.Build(input, reader)

	alice := findJob(jobs, 1)
	if alice == nil {
		t.Fatal("alice should receive bob's entered")
	}
	if len(alice.changes.Entered) != 1 {
		t.Fatalf("alice should have 1 entered, got %d", len(alice.changes.Entered))
	}
	if alice.changes.Entered[0].ID != 2 {
		t.Errorf("entered player should be bob(2), got %d", alice.changes.Entered[0].ID)
	}
}

func TestBuildLeftPlayerFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		PendingLeft: map[int64][]int64{
			1: {2, 3},
		},
	}
	jobs := builder.Build(input, reader)

	alice := findJob(jobs, 1)
	if alice == nil {
		t.Fatal("alice should have changes")
	}
	if len(alice.changes.LeftPlayerIDs) != 2 {
		t.Fatalf("alice should have 2 left IDs, got %d", len(alice.changes.LeftPlayerIDs))
	}
}

func TestBuildAppearanceFanout(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	appearance := game.Appearance{Color: "#ff0000", Shape: "square"}
	input := ReplicationBuildInput{
		PendingAppearances: map[int64]game.Appearance{
			1: appearance,
		},
	}
	jobs := builder.Build(input, reader)

	alice := findJob(jobs, 1)
	if alice == nil || len(alice.changes.Appearances) != 1 {
		t.Fatal("alice should have 1 appearance")
	}
	if alice.changes.Appearances[0].PlayerID != 1 {
		t.Errorf("appearance should reference alice(1), got %d", alice.changes.Appearances[0].PlayerID)
	}

	bob := findJob(jobs, 2)
	if bob == nil || len(bob.changes.Appearances) != 1 {
		t.Fatal("bob should have 1 appearance")
	}
}

// --- Collectible fanout tests ---

func TestBuildCollectibleEnteredFanout(t *testing.T) {
	_, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: game.NewWorld(game.DefaultConfig())}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		CollectEntered: map[int64][]CollectibleEnteredItem{
			1: {{ID: 100, Lat: 31.23, Lng: 121.47}},
		},
	}
	jobs := builder.Build(input, reader)

	alice := findJob(jobs, 1)
	if alice == nil || len(alice.changes.CollectiblesEntered) != 1 {
		t.Fatal("alice should have 1 collectible entered")
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
	jobs := builder.Build(input, reader)

	alice := findJob(jobs, 1)
	if alice == nil || len(alice.changes.CollectibleIDsLeft) != 2 {
		t.Fatal("alice should have 2 collectibles left")
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
	jobs := builder.Build(input, reader)

	alice := findJob(jobs, 1)
	if alice == nil || len(alice.changes.CollectiblesSpawned) != 1 {
		t.Fatal("alice should have 1 collectible spawned")
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
	jobs := builder.Build(input, reader)

	alice := findJob(jobs, 1)
	if alice == nil || len(alice.changes.CollectibleIDsCollected) != 1 {
		t.Fatal("alice should have 1 collectible collected")
	}
}

func TestBuildCollectibleSkipsDisconnected(t *testing.T) {
	_, aoi, clients := playerFanoutFixture()
	delete(clients, 2)
	reader := &concreteReader{clients: clients, aoi: aoi, world: game.NewWorld(game.DefaultConfig())}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		CollectEntered: map[int64][]CollectibleEnteredItem{
			2: {{ID: 100, Lat: 31.23, Lng: 121.47}},
		},
	}
	jobs := builder.Build(input, reader)

	if findJob(jobs, 2) != nil {
		t.Error("disconnected bob should have no collectible changes")
	}
}

// --- Job output tests ---

func TestBuildReturnsEmptyJobsForEmptyInput(t *testing.T) {
	_, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: game.NewWorld(game.DefaultConfig())}
	var builder ReplicationBuilder

	jobs := builder.Build(ReplicationBuildInput{}, reader)
	if len(jobs) != 0 {
		t.Errorf("empty input should produce empty jobs, got %d", len(jobs))
	}
}

func TestBuildDisconnectedRecipientSkipsJob(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	delete(clients, 1) // alice 断连
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	input := ReplicationBuildInput{
		MovedIDs: []int64{1},
	}
	jobs := builder.Build(input, reader)

	// alice 断连，不会产生 job
	if findJob(jobs, 1) != nil {
		t.Error("disconnected alice should not get a job")
	}
}

func TestBuildJobsAreImmutable(t *testing.T) {
	world, aoi, clients := playerFanoutFixture()
	reader := &concreteReader{clients: clients, aoi: aoi, world: world}
	var builder ReplicationBuilder

	// bob(2) 对 alice(1) 相互可见
	entered := []game.PlayerState{
		{ID: 2, Username: "bob", Lat: 31.2305, Lng: 121.4738},
	}
	input := ReplicationBuildInput{
		PendingEntered: entered,
	}
	jobs := builder.Build(input, reader)

	// 修改原始输入 slice
	entered[0].Lat = 0

	// job 里的数据不应受影响
	alice := findJob(jobs, 1)
	if alice == nil {
		t.Fatal("alice should have a job")
	}
	if len(alice.changes.Entered) != 1 {
		t.Fatal("alice should have 1 entered")
	}
	if alice.changes.Entered[0].Lat != 31.2305 {
		t.Errorf("job should be immutable, got Lat=%f", alice.changes.Entered[0].Lat)
	}
}
