package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"map-walker/internal/game"
)

func TestHubRegisterSendsSnapshotAndDefersDelta(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	if !hub.Register(alice) {
		t.Fatal("register failed")
	}
	snapshot := mustReceiveSnapshot(t, alice)
	if len(snapshot.Players) != 1 || snapshot.Players[0].ID != "alice" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	delta := mustReceiveDelta(t, alice)
	if len(delta.Players) != 1 || delta.Players[0].ID != "alice" {
		t.Fatalf("expected deferred alice delta, got %+v", delta)
	}
}

func TestHubSimulationDoesNotBroadcastUntilBroadcastTick(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	delta := mustReceiveDelta(t, alice)
	if delta.Tick != 1 || len(delta.Players) != 1 {
		t.Fatalf("unexpected movement delta: %+v", delta)
	}
}

func TestHubEmptyBroadcastTickSendsNothing(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
}

func TestHubDisconnectAppearsInNextDelta(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	bob := NewTestClient("bob", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	hub.Register(bob)
	mustReceiveSnapshot(t, bob)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)
	mustReceiveDelta(t, bob)

	hub.Unregister(bob)
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	delta := mustReceiveDelta(t, alice)
	if len(delta.RemovedPlayerIDs) != 1 || delta.RemovedPlayerIDs[0] != "bob" {
		t.Fatalf("unexpected removals: %+v", delta.RemovedPlayerIDs)
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
	snapshot := mustReceiveSnapshot(t, alice)
	if len(snapshot.Players) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.Players[0].Lat != 31.5 || snapshot.Players[0].Lng != 121.5 {
		t.Fatalf("expected saved position, got %+v", snapshot.Players[0])
	}
	if snapshot.Players[0].Appearance != savedAppearance {
		t.Fatalf("expected saved appearance, got %+v", snapshot.Players[0].Appearance)
	}
	if snapshot.Players[0].Username != "Alice" {
		t.Fatalf("expected saved username, got %q", snapshot.Players[0].Username)
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
	mustReceiveSnapshot(t, old)
	broadcasts <- time.Now()
	mustReceiveDelta(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, old)
	broadcasts <- time.Now()
	moved := mustReceiveDelta(t, old)
	movedLng := moved.Players[0].Lng

	hub.Register(replacement)
	snapshot := mustReceiveSnapshot(t, replacement)
	if snapshot.Players[0].Lng != movedLng {
		t.Fatalf("replacement reloaded stale saved position: got %v want %v", snapshot.Players[0].Lng, movedLng)
	}
}

func TestHubReplacementRetainsInMemoryPosition(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient("alice", 8)
	replacement := NewTestClient("alice", 8)
	hub.Register(old)
	mustReceiveSnapshot(t, old)
	broadcasts <- time.Now()
	mustReceiveDelta(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, old)
	broadcasts <- time.Now()
	moved := mustReceiveDelta(t, old)
	if len(moved.Players) != 1 {
		t.Fatalf("unexpected movement delta: %+v", moved)
	}
	movedLng := moved.Players[0].Lng
	if movedLng <= testWorldConfig().SpawnLng {
		t.Fatalf("expected player to move right from spawn, got %+v", moved.Players[0])
	}

	hub.Register(replacement)
	snapshot := mustReceiveSnapshot(t, replacement)
	if len(snapshot.Players) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.Players[0].Lng != movedLng {
		t.Fatalf("replacement reset position: got %v want %v", snapshot.Players[0].Lng, movedLng)
	}

	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	assertNoMessage(t, replacement)
	broadcasts <- time.Now()
	replacementDelta := mustReceiveDelta(t, replacement)
	if replacementDelta.Players[0].Lng >= movedLng {
		t.Fatalf("replacement connection did not control player: %+v", replacementDelta.Players[0])
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
	mustReceiveSnapshot(t, old)
	broadcasts <- time.Now()
	mustReceiveDelta(t, old)

	hub.Register(replacement)
	mustReceiveSnapshot(t, replacement)

	hub.Unregister(old)
	broadcasts <- time.Now()
	assertNoMessage(t, replacement)

	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, replacement)
	broadcasts <- time.Now()

	delta := mustReceiveDelta(t, replacement)
	if len(delta.RemovedPlayerIDs) != 0 {
		t.Fatalf("replacement removed: %+v", delta.RemovedPlayerIDs)
	}
}

func TestHubRejectsInputFromReplacedConnection(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient("alice", 8)
	replacement := NewTestClient("alice", 8)
	hub.Register(old)
	mustReceiveSnapshot(t, old)
	broadcasts <- time.Now()
	mustReceiveDelta(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	hub.Register(replacement)
	mustReceiveSnapshot(t, replacement)
	hub.ApplyInput(old, game.InputState{Sequence: 2, Right: true})
	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	delta := mustReceiveDelta(t, replacement)
	if len(delta.Players) != 1 {
		t.Fatalf("unexpected replacement delta: %+v", delta)
	}
	if delta.Players[0].Lng >= testWorldConfig().SpawnLng {
		t.Fatalf("stale old connection controlled player: %+v", delta.Players[0])
	}
	if len(delta.RemovedPlayerIDs) != 0 {
		t.Fatalf("replacement must not emit removal: %+v", delta.RemovedPlayerIDs)
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
	mustReceiveSnapshot(t, fast)
	broadcasts <- time.Now()
	mustReceiveDelta(t, fast)

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
	mustReceiveSnapshot(t, alice)
	hub.Register(bob)
	mustReceiveSnapshot(t, bob)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)
	mustReceiveDelta(t, bob)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance("alice", updated) {
		t.Fatal("appearance update failed")
	}

	aliceMessage := mustReceiveAppearanceChanged(t, alice)
	bobMessage := mustReceiveAppearanceChanged(t, bob)
	if aliceMessage.PlayerID != "alice" || aliceMessage.Appearance != updated {
		t.Fatalf("unexpected alice appearance message: %+v", aliceMessage)
	}
	if bobMessage != aliceMessage {
		t.Fatalf("clients received different appearance messages: %+v %+v", aliceMessage, bobMessage)
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
	snapshot := mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)

	if !hub.UpdateAppearance("alice", snapshot.Players[0].Appearance) {
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
	mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)

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
	initial := mustReceiveSnapshot(t, old)
	broadcasts <- time.Now()
	mustReceiveDelta(t, old)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance("alice", updated) {
		t.Fatal("appearance update failed")
	}
	mustReceiveAppearanceChanged(t, old)

	hub.Register(replacement)
	snapshot := mustReceiveSnapshot(t, replacement)
	if snapshot.Players[0].Appearance != updated {
		t.Fatalf("replacement reloaded stale appearance: got %+v want %+v", snapshot.Players[0].Appearance, updated)
	}
	if snapshot.Players[0].Appearance == initial.Players[0].Appearance {
		t.Fatalf("expected appearance to change before replacement: %+v", initial.Players[0].Appearance)
	}
}

func TestHubDisconnectUserRemovesPlayer(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	bob := NewTestClient("bob", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	hub.Register(bob)
	mustReceiveSnapshot(t, bob)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)
	mustReceiveDelta(t, bob)

	hub.DisconnectUser("alice")

	broadcasts <- time.Now()
	delta := mustReceiveDelta(t, bob)
	if len(delta.RemovedPlayerIDs) != 1 || delta.RemovedPlayerIDs[0] != "alice" {
		t.Fatalf("expected alice removed, got %+v", delta)
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

func mustReceiveSnapshot(t *testing.T, client *testClient) WorldSnapshotMessage {
	t.Helper()
	data := mustReceiveData(t, client)
	var message WorldSnapshotMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode snapshot failed: %v", err)
	}
	if message.Type != MessageTypeWorldSnapshot {
		t.Fatalf("expected snapshot, got %q", message.Type)
	}
	return message
}

func mustReceiveAppearanceChanged(t *testing.T, client *testClient) AppearanceChangedMessage {
	t.Helper()
	data := mustReceiveData(t, client)
	var message AppearanceChangedMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode appearance changed failed: %v", err)
	}
	if message.Type != MessageTypeAppearanceChanged {
		t.Fatalf("expected appearance changed, got %q", message.Type)
	}
	return message
}

func mustReceiveDelta(t *testing.T, client *testClient) PlayersDeltaMessage {
	t.Helper()
	data := mustReceiveData(t, client)
	var message PlayersDeltaMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode delta failed: %v", err)
	}
	if message.Type != MessageTypePlayersDelta {
		t.Fatalf("expected delta, got %q", message.Type)
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
