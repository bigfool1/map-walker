package realtime

import (
	"sync"
	"testing"
	"time"

	"map-walker/internal/game"
)

type recordingPersister struct {
	mu      sync.Mutex
	batches [][]PositionUpdate
}

func (r *recordingPersister) Submit(updates []PositionUpdate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, append([]PositionUpdate(nil), updates...))
}

func (r *recordingPersister) Stop() {}

func waitForClientClose(t *testing.T, client *testClient) {
	t.Helper()
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client close")
	}
}

func waitForPersistBatches(t *testing.T, persister *recordingPersister, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(persister.Batches()) >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d batches, got %d", count, len(persister.Batches()))
}

func (r *recordingPersister) Batches() [][]PositionUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]PositionUpdate, len(r.batches))
	copy(out, r.batches)
	return out
}

type blockingPersister struct {
	release chan struct{}
	started chan struct{}
}

func newBlockingPersister() *blockingPersister {
	return &blockingPersister{
		release: make(chan struct{}),
		started: make(chan struct{}, 8),
	}
}

func (b *blockingPersister) Submit(updates []PositionUpdate) {
	go func() {
		b.started <- struct{}{}
		<-b.release
	}()
}

func (b *blockingPersister) Stop() {}

func TestHubPersistenceBatchIncludesOnlyMovedPlayers(t *testing.T) {
	persister := &recordingPersister{}
	hub, simulations, broadcasts, persistence := newTestHubWithLoader(nil, persister)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	bob := NewTestClient("bob", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	hub.Register(bob)
	mustReceiveSnapshot(t, bob)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)
	assertNoMessage(t, bob)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()
	moved := mustReceiveDelta(t, alice)
	mustReceiveDelta(t, bob)

	persistence <- time.Now()
	waitForPersistBatches(t, persister, 1)

	batches := persister.Batches()
	if len(batches) != 1 {
		t.Fatalf("expected one batch, got %d", len(batches))
	}
	if len(batches[0]) != 1 || batches[0][0].UserID != "alice" {
		t.Fatalf("expected only alice in batch, got %+v", batches[0])
	}
	if batches[0][0].Lng != moved.Players[0].Lng {
		t.Fatalf("expected moved alice position, got %+v", batches[0][0])
	}
}

func TestHubPersistenceSkipsUnchangedPlayers(t *testing.T) {
	persister := &recordingPersister{}
	hub, _, broadcasts, persistence := newTestHubWithLoader(nil, persister)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	persistence <- time.Now()

	if batches := persister.Batches(); len(batches) != 0 {
		t.Fatalf("expected no persistence batch for idle player, got %+v", batches)
	}
}

func TestHubFinalSaveOnGenuineDisconnect(t *testing.T) {
	persister := &recordingPersister{}
	hub, simulations, broadcasts, _ := newTestHubWithLoader(nil, persister)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()
	moved := mustReceiveDelta(t, alice)

	hub.Unregister(alice)
	waitForClientClose(t, alice)

	batches := persister.Batches()
	if len(batches) != 1 {
		t.Fatalf("expected final save batch, got %d", len(batches))
	}
	if len(batches[0]) != 1 || batches[0][0].UserID != "alice" {
		t.Fatalf("unexpected final save: %+v", batches[0])
	}
	if batches[0][0].Lng != moved.Players[0].Lng {
		t.Fatalf("expected moved final position, got %+v", batches[0][0])
	}
}

func TestHubReplacementDoesNotFinalSave(t *testing.T) {
	persister := &recordingPersister{}
	hub, simulations, broadcasts, _ := newTestHubWithLoader(nil, persister)
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient("alice", 8)
	replacement := NewTestClient("alice", 8)
	hub.Register(old)
	mustReceiveSnapshot(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, old)
	broadcasts <- time.Now()
	mustReceiveDelta(t, old)

	hub.Register(replacement)
	mustReceiveSnapshot(t, replacement)
	hub.Unregister(old)

	if batches := persister.Batches(); len(batches) != 0 {
		t.Fatalf("replacement unregister must not final-save, got %+v", batches)
	}
}

func TestHubSimulationContinuesWhilePersistenceBlocks(t *testing.T) {
	persister := newBlockingPersister()
	hub, simulations, broadcasts, persistence := newTestHubWithLoader(nil, persister)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	persistence <- time.Now()

	select {
	case <-persister.started:
	case <-time.After(time.Second):
		t.Fatal("expected persistence submit to start")
	}

	simulations <- time.Now()
	broadcasts <- time.Now()
	delta := mustReceiveDelta(t, alice)
	if len(delta.Players) != 1 {
		t.Fatalf("expected simulation to continue during blocked persistence, got %+v", delta)
	}

	close(persister.release)
}

func TestHubPersistenceUsesIncreasingSequence(t *testing.T) {
	persister := &recordingPersister{}
	hub, simulations, broadcasts, persistence := newTestHubWithLoader(nil, persister)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	persistence <- time.Now()
	waitForPersistBatches(t, persister, 1)

	hub.Unregister(alice)
	waitForClientClose(t, alice)
	waitForPersistBatches(t, persister, 2)

	batches := persister.Batches()
	if len(batches) != 2 {
		t.Fatalf("expected periodic and final batches, got %d", len(batches))
	}
	if batches[0][0].Seq >= batches[1][0].Seq {
		t.Fatalf("expected increasing sequence: %+v then %+v", batches[0][0], batches[1][0])
	}
}
