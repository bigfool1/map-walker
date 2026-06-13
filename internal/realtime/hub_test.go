package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"map-walker/internal/game"
)

func TestHubRegisterSendsInitializationWithoutStaticReplication(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	if !hub.Register(alice) {
		t.Fatal("register failed")
	}
	self, visible := mustReceiveInitialization(t, alice)
	if self.Player.ID != "alice" || len(visible.Players) != 0 {
		t.Fatalf("unexpected initialization: self=%+v visible=%+v", self, visible)
	}
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
}

func TestHubSimulationDoesNotBroadcastUntilBroadcastTick(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	update := mustReceiveReplicationUpdate(t, alice)
	if update.Tick != 1 || update.SelfPosition == nil {
		t.Fatalf("unexpected movement replication: %+v", update)
	}
}

func TestHubEmptyBroadcastTickSendsNothing(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
}

func TestHubDisconnectAppearsInNextReplication(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	bob := NewTestClient("bob", 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	joined := mustReceiveReplicationUpdate(t, alice)
	if len(joined.Entered) != 1 || joined.Entered[0].ID != "bob" {
		t.Fatalf("expected bob entered for alice, got %+v", joined)
	}
	assertNoMessage(t, bob)

	hub.Unregister(bob)
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, alice)
	if len(update.LeftPlayerIDs) != 1 || update.LeftPlayerIDs[0] != "bob" {
		t.Fatalf("unexpected removals: %+v", update.LeftPlayerIDs)
	}
}

func TestHubRestoresOfflinePlayerAtSavedPosition(t *testing.T) {
	savedAppearance := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	loader := SavedPlayerLoader(func(userID string) (SavedPlayerLoad, bool) {
		if userID != "alice" {
			return SavedPlayerLoad{}, false
		}
		return SavedPlayerLoad{
			Lat:         31.5,
			Lng:         121.5,
			HasPosition: true,
			Username:    "Alice",
			Appearance:  savedAppearance,
		}, true
	})

	hub, _, _, _ := newTestHubWithLoader(loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	self, visible := mustReceiveInitialization(t, alice)
	if len(visible.Players) != 0 {
		t.Fatalf("unexpected visible players: %+v", visible)
	}
	if self.Player.Lat != 31.5 || self.Player.Lng != 121.5 {
		t.Fatalf("expected saved position, got %+v", self.Player)
	}
	if self.Player.Appearance != savedAppearance {
		t.Fatalf("expected saved appearance, got %+v", self.Player.Appearance)
	}
	if self.Player.Username != "Alice" {
		t.Fatalf("expected saved username, got %q", self.Player.Username)
	}
}

func TestHubReplacementIgnoresSavedPositionLoader(t *testing.T) {
	loader := SavedPlayerLoader(func(userID string) (SavedPlayerLoad, bool) {
		return SavedPlayerLoad{
			Lat:         31.99,
			Lng:         121.99,
			HasPosition: true,
			Appearance:  game.Appearance{Color: "#000000", Shape: game.ShapeSquare},
		}, true
	})

	hub, simulations, broadcasts, _ := newTestHubWithLoader(loader, nil)
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient("alice", 8)
	replacement := NewTestClient("alice", 8)
	hub.Register(old)
	mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, old)
	broadcasts <- time.Now()
	moved := mustReceiveReplicationUpdate(t, old)
	if moved.SelfPosition == nil {
		t.Fatalf("expected self position replication: %+v", moved)
	}
	movedLng := moved.SelfPosition.Lng

	hub.Register(replacement)
	self, _ := mustReceiveInitialization(t, replacement)
	if self.Player.Lng != movedLng {
		t.Fatalf("replacement reloaded stale saved position: got %v want %v", self.Player.Lng, movedLng)
	}
}

func TestHubReplacementRetainsInMemoryPosition(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient("alice", 8)
	replacement := NewTestClient("alice", 8)
	hub.Register(old)
	mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, old)
	broadcasts <- time.Now()
	moved := mustReceiveReplicationUpdate(t, old)
	if moved.SelfPosition == nil {
		t.Fatalf("unexpected movement replication: %+v", moved)
	}
	movedLng := moved.SelfPosition.Lng
	if movedLng <= testWorldConfig().SpawnLng {
		t.Fatalf("expected player to move right from spawn, got %+v", moved.SelfPosition)
	}

	hub.Register(replacement)
	self, _ := mustReceiveInitialization(t, replacement)
	if self.Player.Lng != movedLng {
		t.Fatalf("replacement reset position: got %v want %v", self.Player.Lng, movedLng)
	}

	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	assertNoMessage(t, replacement)
	broadcasts <- time.Now()
	replacementUpdate := mustReceiveReplicationUpdate(t, replacement)
	if replacementUpdate.SelfPosition == nil || replacementUpdate.SelfPosition.Lng >= movedLng {
		t.Fatalf("replacement connection did not control player: %+v", replacementUpdate.SelfPosition)
	}

	hub.Unregister(old)
	broadcasts <- time.Now()
	assertNoMessage(t, replacement)
}

func TestHubReplacementSurvivesObsoleteUnregister(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient("alice", 8)
	replacement := NewTestClient("alice", 8)
	hub.Register(old)
	mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	hub.Register(replacement)
	mustReceiveInitialization(t, replacement)

	hub.Unregister(old)
	broadcasts <- time.Now()
	assertNoMessage(t, replacement)

	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, replacement)
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, replacement)
	if len(update.LeftPlayerIDs) != 0 {
		t.Fatalf("replacement removed: %+v", update.LeftPlayerIDs)
	}
}

func TestHubRejectsInputFromReplacedConnection(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient("alice", 8)
	replacement := NewTestClient("alice", 8)
	hub.Register(old)
	mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	hub.Register(replacement)
	mustReceiveInitialization(t, replacement)
	hub.ApplyInput(old, game.InputState{Sequence: 2, Right: true})
	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, replacement)
	if update.SelfPosition == nil {
		t.Fatalf("unexpected replacement replication: %+v", update)
	}
	if update.SelfPosition.Lng >= testWorldConfig().SpawnLng {
		t.Fatalf("stale old connection controlled player: %+v", update.SelfPosition)
	}
	if len(update.LeftPlayerIDs) != 0 {
		t.Fatalf("replacement must not emit removal: %+v", update.LeftPlayerIDs)
	}

	hub.Unregister(old)
	broadcasts <- time.Now()
	assertNoMessage(t, replacement)
}

func TestHubDropsSlowClient(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	slow := NewTestClient("slow", 0)
	fast := NewTestClient("fast", 8)
	hub.Register(slow)
	hub.Register(fast)
	mustReceiveInitialization(t, fast)
	broadcasts <- time.Now()
	update := mustReceiveReplicationUpdate(t, fast)
	if len(update.LeftPlayerIDs) != 1 || update.LeftPlayerIDs[0] != "slow" {
		t.Fatalf("expected slow removal broadcast, got %+v", update)
	}

	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("expected slow client to close")
	}
}

func TestHubMethodsReturnAfterStop(t *testing.T) {
	hub, _, _, _ := newTestHub()
	go hub.Run()
	hub.Stop()

	client := NewTestClient("alice", 1)
	if hub.Register(client) {
		t.Fatal("register should fail after stop")
	}
	if hub.ApplyInput(client, game.InputState{Sequence: 1, Up: true}) {
		t.Fatal("input should fail after stop")
	}
	if hub.UpdateAppearance("alice", game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}) {
		t.Fatal("appearance update should fail after stop")
	}
	hub.Unregister(client)
}

func TestHubUpdateAppearanceBroadcastsToAllClients(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	bob := NewTestClient("bob", 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance("alice", updated) {
		t.Fatal("appearance update failed")
	}

	broadcasts <- time.Now()
	aliceUpdate := mustReceiveReplicationUpdate(t, alice)
	bobUpdate := mustReceiveReplicationUpdate(t, bob)
	if len(aliceUpdate.Appearances) != 1 || aliceUpdate.Appearances[0].PlayerID != "alice" || aliceUpdate.Appearances[0].Appearance != updated {
		t.Fatalf("unexpected alice appearance replication: %+v", aliceUpdate)
	}
	if len(bobUpdate.Appearances) != 1 || bobUpdate.Appearances[0] != aliceUpdate.Appearances[0] {
		t.Fatalf("clients received different appearance replication: %+v %+v", aliceUpdate, bobUpdate)
	}

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
	assertNoMessage(t, bob)
}

func TestHubUpdateAppearanceUnchangedDoesNotBroadcast(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	self, _ := mustReceiveInitialization(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	if !hub.UpdateAppearance("alice", self.Player.Appearance) {
		t.Fatal("unchanged appearance update failed")
	}
	assertNoMessage(t, alice)
}

func TestHubUpdateAppearanceOfflineUserSucceedsWithoutBroadcast(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeTriangle}
	if !hub.UpdateAppearance("offline-user", updated) {
		t.Fatal("offline appearance update failed")
	}
	assertNoMessage(t, alice)
}

func TestHubReplacementRetainsInMemoryAppearance(t *testing.T) {
	loader := SavedPlayerLoader(func(userID string) (SavedPlayerLoad, bool) {
		return SavedPlayerLoad{
			Lat:         31.99,
			Lng:         121.99,
			HasPosition: true,
			Appearance:  game.Appearance{Color: "#000000", Shape: game.ShapeSquare},
		}, true
	})

	hub, _, broadcasts, _ := newTestHubWithLoader(loader, nil)
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient("alice", 8)
	replacement := NewTestClient("alice", 8)
	hub.Register(old)
	initial, _ := mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance("alice", updated) {
		t.Fatal("appearance update failed")
	}
	broadcasts <- time.Now()
	appearanceUpdate := mustReceiveReplicationUpdate(t, old)
	if len(appearanceUpdate.Appearances) != 1 || appearanceUpdate.Appearances[0].Appearance != updated {
		t.Fatalf("unexpected appearance replication: %+v", appearanceUpdate)
	}

	hub.Register(replacement)
	self, _ := mustReceiveInitialization(t, replacement)
	if self.Player.Appearance != updated {
		t.Fatalf("replacement reloaded stale appearance: got %+v want %+v", self.Player.Appearance, updated)
	}
	if self.Player.Appearance == initial.Player.Appearance {
		t.Fatalf("expected appearance to change before replacement: %+v", initial.Player.Appearance)
	}
}

func TestHubDisconnectUserRemovesPlayer(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	bob := NewTestClient("bob", 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.DisconnectUser("alice")

	broadcasts <- time.Now()
	update := mustReceiveReplicationUpdate(t, bob)
	if len(update.LeftPlayerIDs) != 1 || update.LeftPlayerIDs[0] != "alice" {
		t.Fatalf("expected alice removed, got %+v", update)
	}

	select {
	case <-alice.done:
	case <-time.After(time.Second):
		t.Fatal("expected alice client to close after disconnect")
	}
}

func TestHubDisconnectUserUnknownIDIsNoop(t *testing.T) {
	hub, _, _, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	hub.DisconnectUser("nonexistent")
}

func TestHubDisconnectUserDoesNotBlockAfterStop(t *testing.T) {
	hub, _, _, _ := newTestHub()
	go hub.Run()
	hub.Stop()

	done := make(chan struct{})
	go func() {
		hub.DisconnectUser("alice")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DisconnectUser blocked after stop")
	}
}

func newTestHub() (*Hub, chan time.Time, chan time.Time, chan time.Time) {
	return newTestHubWithLoader(nil, nil)
}

func newTestHubWithLoader(loader SavedPlayerLoader, persister PositionPersister) (*Hub, chan time.Time, chan time.Time, chan time.Time) {
	simulations := make(chan time.Time, 8)
	broadcasts := make(chan time.Time, 8)
	persistence := make(chan time.Time, 8)
	world := game.NewWorld(testWorldConfig())
	hub := newHub(world, loader, persister, simulations, broadcasts, persistence, nil, func() {})
	return hub, simulations, broadcasts, persistence
}

func testWorldConfig() game.Config {
	return game.Config{
		SpawnLat:             31.2304,
		SpawnLng:             121.4737,
		SpeedMetersPerSecond: 12,
	}
}

type testClient struct {
	id   string
	send chan []byte
	done chan struct{}
}

func NewTestClient(id string, buffer int) *testClient {
	return &testClient{
		id:   id,
		send: make(chan []byte, buffer),
		done: make(chan struct{}),
	}
}

func (c *testClient) ID() string {
	return c.id
}

func (c *testClient) Username() string {
	return c.id
}

func (c *testClient) Send(data []byte) bool {
	select {
	case c.send <- data:
		return true
	default:
		return false
	}
}

func (c *testClient) CloseSend() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

func mustReceiveInitialization(t *testing.T, client *testClient) (SelfStateMessage, VisibleEntitiesSnapshotMessage) {
	t.Helper()
	var self SelfStateMessage
	if err := json.Unmarshal(mustReceiveData(t, client), &self); err != nil {
		t.Fatalf("decode self state failed: %v", err)
	}
	if self.Type != MessageTypeSelfState {
		t.Fatalf("expected self state, got %q", self.Type)
	}

	var visible VisibleEntitiesSnapshotMessage
	if err := json.Unmarshal(mustReceiveData(t, client), &visible); err != nil {
		t.Fatalf("decode visible entities snapshot failed: %v", err)
	}
	if visible.Type != MessageTypeVisibleEntitiesSnapshot {
		t.Fatalf("expected visible entities snapshot, got %q", visible.Type)
	}
	return self, visible
}

func mustReceiveReplicationUpdate(t *testing.T, client *testClient) ReplicationUpdateMessage {
	t.Helper()
	data := mustReceiveData(t, client)
	var message ReplicationUpdateMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode replication update failed: %v", err)
	}
	if message.Type != MessageTypeReplicationUpdate {
		t.Fatalf("expected replication update, got %q", message.Type)
	}
	return message
}

func mustReceiveData(t *testing.T, client *testClient) []byte {
	t.Helper()
	select {
	case data := <-client.send:
		return data
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

func assertNoMessage(t *testing.T, client *testClient) {
	t.Helper()
	select {
	case data := <-client.send:
		t.Fatalf("unexpected message: %s", data)
	case <-time.After(20 * time.Millisecond):
	}
}
