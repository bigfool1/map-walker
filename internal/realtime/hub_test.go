package realtime

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHubRegistersClientAndSendsInitialSnapshot(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	client := NewTestClient("alice", 4)
	hub.Register(client)

	msg := mustReceiveSnapshot(t, client)
	if len(msg.Players) != 0 {
		t.Fatalf("expected empty initial snapshot, got %+v", msg.Players)
	}
}

func TestHubBroadcastsPositionUpdates(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 4)
	bob := NewTestClient("bob", 4)
	hub.Register(alice)
	hub.Register(bob)
	mustReceiveSnapshot(t, alice)
	mustReceiveSnapshot(t, bob)

	hub.UpdatePosition(PositionUpdateMessage{
		Type:     MessageTypePositionUpdate,
		PlayerID: "alice",
		Lat:      31.2304,
		Lng:      121.4737,
	})

	aliceSnapshot := mustReceiveSnapshot(t, alice)
	bobSnapshot := mustReceiveSnapshot(t, bob)

	for _, snapshot := range []PlayersSnapshotMessage{aliceSnapshot, bobSnapshot} {
		if len(snapshot.Players) != 1 {
			t.Fatalf("expected 1 player, got %+v", snapshot.Players)
		}
		if snapshot.Players[0].ID != "alice" {
			t.Fatalf("expected alice, got %+v", snapshot.Players[0])
		}
	}
}

func TestHubUnregisterRemovesPlayerAndBroadcasts(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 4)
	bob := NewTestClient("bob", 4)
	hub.Register(alice)
	hub.Register(bob)
	mustReceiveSnapshot(t, alice)
	mustReceiveSnapshot(t, bob)

	hub.UpdatePosition(PositionUpdateMessage{Type: MessageTypePositionUpdate, PlayerID: "alice", Lat: 1, Lng: 2})
	mustReceiveSnapshot(t, alice)
	mustReceiveSnapshot(t, bob)

	hub.Unregister(alice)

	msg := mustReceiveSnapshot(t, bob)
	if len(msg.Players) != 0 {
		t.Fatalf("expected alice to be removed, got %+v", msg.Players)
	}
}

func TestHubDropsSlowClient(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	slow := NewTestClient("slow", 0)
	fast := NewTestClient("fast", 8)
	hub.Register(slow)
	hub.Register(fast)
	mustReceiveSnapshot(t, fast)

	hub.UpdatePosition(PositionUpdateMessage{Type: MessageTypePositionUpdate, PlayerID: "fast", Lat: 1, Lng: 2})
	mustReceiveSnapshot(t, fast)

	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("expected slow client to be closed")
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

func mustReceiveSnapshot(t *testing.T, client *testClient) PlayersSnapshotMessage {
	t.Helper()

	select {
	case data := <-client.send:
		var msg PlayersSnapshotMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("decode snapshot failed: %v", err)
		}
		if msg.Type != MessageTypePlayersSnapshot {
			t.Fatalf("expected snapshot message, got %q", msg.Type)
		}
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
		return PlayersSnapshotMessage{}
	}
}
