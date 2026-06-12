package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"map-walker/internal/game"
)

func TestHubRegisterSendsSnapshotAndDefersDelta(t *testing.T) {
	hub, _, broadcasts := newTestHub()
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
	hub, simulations, broadcasts := newTestHub()
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
	hub, _, broadcasts := newTestHub()
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
	hub, _, broadcasts := newTestHub()
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
	loader := SavedPositionLoader(func(userID string) (float64, float64, bool) {
		if userID != "alice" {
			return 0, 0, false
		}
		return 31.5, 121.5, true
	})

	hub, _, _ := newTestHubWithLoader(loader)
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
}

func TestHubReplacementIgnoresSavedPositionLoader(t *testing.T) {
	loader := SavedPositionLoader(func(userID string) (float64, float64, bool) {
		return 31.99, 121.99, true
	})

	hub, simulations, broadcasts := newTestHubWithLoader(loader)
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
	hub, simulations, broadcasts := newTestHub()
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
	hub, simulations, broadcasts := newTestHub()
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
	hub, simulations, broadcasts := newTestHub()
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
	hub, _, broadcasts := newTestHub()
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
	hub, _, _ := newTestHub()
	go hub.Run()
	hub.Stop()

	client := NewTestClient("alice", 1)
	if hub.Register(client) {
		t.Fatal("register should fail after stop")
	}
	if hub.ApplyInput(client, game.InputState{Sequence: 1, Up: true}) {
		t.Fatal("input should fail after stop")
	}
	hub.Unregister(client)
}

func newTestHub() (*Hub, chan time.Time, chan time.Time) {
	return newTestHubWithLoader(nil)
}

func newTestHubWithLoader(loader SavedPositionLoader) (*Hub, chan time.Time, chan time.Time) {
	simulations := make(chan time.Time, 8)
	broadcasts := make(chan time.Time, 8)
	world := game.NewWorld(testWorldConfig())
	hub := newHub(world, loader, simulations, broadcasts, nil, func() {})
	return hub, simulations, broadcasts
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
