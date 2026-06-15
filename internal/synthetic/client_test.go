package synthetic

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"map-walker/internal/realtime"
)

func TestClientUsesSharedSendBufferCapacity(t *testing.T) {
	client := NewClientWithHeldDrain(1, "synthetic_1", realtime.DefaultSendBufferSize)
	defer client.CloseSend()

	payload := []byte("x")
	for i := 0; i < realtime.DefaultSendBufferSize; i++ {
		if !client.Send(payload) {
			t.Fatalf("send %d failed while queue should accept %d messages", i, realtime.DefaultSendBufferSize)
		}
	}
	if client.Send(payload) {
		t.Fatal("expected queue full without drain")
	}
}

func TestClientBecomesReadyAfterFourInitMessages(t *testing.T) {
	client := NewClient(1, "synthetic_1")
	defer client.CloseSend()

	if !client.Send([]byte("self_state")) ||
		!client.Send([]byte("visible_entities_snapshot")) ||
		!client.Send([]byte("collectible_regions")) ||
		!client.Send([]byte("visible_collectibles_snapshot")) {
		t.Fatal("expected initialization sends to succeed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady failed: %v", err)
	}
	if client.MessagesDrained() != 4 || client.BytesDrained() == 0 {
		t.Fatalf("unexpected drain counters: messages=%d bytes=%d", client.MessagesDrained(), client.BytesDrained())
	}
}

func TestClientEarlyCloseFailsReadiness(t *testing.T) {
	client := NewClient(1, "synthetic_1")
	if !client.Send([]byte("self_state")) {
		t.Fatal("expected first send to succeed")
	}
	client.CloseSend()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitReady(ctx); !errors.Is(err, ErrClosedBeforeReady) {
		t.Fatalf("WaitReady = %v, want ErrClosedBeforeReady", err)
	}
	if err := client.WaitDone(ctx); err != nil {
		t.Fatalf("WaitDone failed: %v", err)
	}
}

func TestClientReadinessTimeout(t *testing.T) {
	client := NewClient(1, "synthetic_1")
	defer client.CloseSend()

	if !client.Send([]byte("self_state")) {
		t.Fatal("expected first send to succeed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := client.WaitReady(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitReady = %v, want deadline exceeded", err)
	}
}

func TestClientTracksQueueHighWater(t *testing.T) {
	client := newSlowDrainClient(50 * time.Millisecond)
	defer client.CloseSend()

	payload := []byte("12345")
	for i := 0; i < 4; i++ {
		if !client.Send(payload) {
			t.Fatalf("send %d failed", i)
		}
	}

	deadline := time.Now().Add(time.Second)
	for client.QueueHighWater() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("queue high-water = %d, want at least 2", client.QueueHighWater())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestClientCloseSendIsIdempotent(t *testing.T) {
	client := NewClient(1, "synthetic_1")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		client.CloseSend()
	}()
	go func() {
		defer wg.Done()
		client.CloseSend()
	}()
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitDone(ctx); err != nil {
		t.Fatalf("WaitDone failed: %v", err)
	}
}

func TestClientSendCloseRace(t *testing.T) {
	for i := 0; i < 100; i++ {
		client := NewClient(1, "synthetic_1")
		done := make(chan struct{})
		go func() {
			defer close(done)
			for j := 0; j < 32; j++ {
				client.Send([]byte("payload"))
			}
			client.CloseSend()
		}()
		<-done
		if err := client.WaitDone(context.Background()); err != nil {
			t.Fatalf("iteration %d: WaitDone failed: %v", i, err)
		}
	}
}

func newSlowDrainClient(delay time.Duration) *Client {
	client := NewClient(1, "synthetic_1")
	client.drainDelay = delay
	return client
}
