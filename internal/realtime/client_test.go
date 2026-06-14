package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func withFastHeartbeat(t *testing.T) {
	t.Helper()
	restore := setHeartbeatTiming(50*time.Millisecond, 30*time.Millisecond)
	t.Cleanup(restore)
}

func setHeartbeatTiming(interval, timeout time.Duration) func() {
	oldInterval := pingInterval
	oldTimeout := pingTimeout
	pingInterval = interval
	pingTimeout = timeout
	return func() {
		pingInterval = oldInterval
		pingTimeout = oldTimeout
	}
}

func TestClientDisconnectsUnresponsivePeer(t *testing.T) {
	withFastHeartbeat(t)

	hub, _, _, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept failed: %v", err)
			return
		}

		client := NewClient(1001, "alice", conn, hub)
		go func() {
			client.Run(context.Background())
			close(closed)
		}()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	peer, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer peer.CloseNow()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for unresponsive peer disconnect")
	}
}

func TestClientKeepsResponsivePeerConnected(t *testing.T) {
	withFastHeartbeat(t)

	hub, _, _, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept failed: %v", err)
			return
		}

		client := NewClient(1001, "alice", conn, hub)
		go client.Run(context.Background())
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	peer, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer peer.CloseNow()

	readCtx, stopRead := context.WithCancel(context.Background())
	defer stopRead()
	go func() {
		for {
			if _, _, err := peer.Read(readCtx); err != nil {
				return
			}
		}
	}()

	time.Sleep(250 * time.Millisecond)

	pingCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := peer.Ping(pingCtx); err != nil {
		t.Fatalf("responsive peer disconnected: %v", err)
	}
}

func TestClientFinishStopsHeartbeatWorker(t *testing.T) {
	withFastHeartbeat(t)

	hub, _, _, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept failed: %v", err)
			return
		}

		client := NewClient(1001, "alice", conn, hub)
		go func() {
			client.Run(context.Background())
			close(done)
		}()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	peer, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	go func() {
		for {
			if _, _, err := peer.Read(context.Background()); err != nil {
				return
			}
		}
	}()

	time.Sleep(30 * time.Millisecond)
	peer.CloseNow()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("client lifecycle did not complete after close")
	}
}
