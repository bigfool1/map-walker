package synthetic

import (
	"sync"
	"testing"
	"time"
)

func TestSnapshotNilBeforeFirstTick(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount: 1,
		accounts:    []testAccount{{accountNumber: 1}},
	})
	defer env.cleanup()

	if env.manager.Snapshot() != nil {
		t.Fatal("expected nil snapshot before first stats tick")
	}
}

func TestSnapshotGaugesAfterActivation(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount: 2,
		accounts: []testAccount{
			{accountNumber: 1},
			{accountNumber: 2},
		},
	})
	defer env.cleanup()

	env.activateAll(t)
	env.tickStats()

	snap := env.manager.Snapshot()
	if snap == nil {
		t.Fatal("expected non-nil snapshot after stats tick")
	}
	if snap.TargetCount != 2 {
		t.Errorf("TargetCount=%d want 2", snap.TargetCount)
	}
	if snap.ActiveCount != 2 {
		t.Errorf("ActiveCount=%d want 2", snap.ActiveCount)
	}
	if snap.ActivatingCount != 0 {
		t.Errorf("ActivatingCount=%d want 0", snap.ActivatingCount)
	}
	if snap.FailedCount != 0 {
		t.Errorf("FailedCount=%d want 0", snap.FailedCount)
	}
	if snap.SampledAt.IsZero() {
		t.Error("SampledAt is zero")
	}
}

func TestSnapshotMovingAndIdleGauges(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount:  2,
		accounts:     []testAccount{{accountNumber: 1}, {accountNumber: 2}},
		fastMovement: true,
	})
	defer env.cleanup()

	env.activateAll(t)

	// Drive enough ticks for at least one client to emit a non-neutral input.
	for i := 0; i < 20; i++ {
		env.tickManager()
		env.driveSimulation()
	}
	env.tickStats()

	snap := env.manager.Snapshot()
	if snap == nil {
		t.Fatal("nil snapshot")
	}
	if snap.MovingCount+snap.IdleCount != snap.ActiveCount {
		t.Errorf("MovingCount(%d)+IdleCount(%d) != ActiveCount(%d)", snap.MovingCount, snap.IdleCount, snap.ActiveCount)
	}
}

func TestSnapshotLifetimeTotalsAccumulate(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount:  1,
		accounts:     []testAccount{{accountNumber: 1}},
		fastMovement: true,
	})
	defer env.cleanup()

	env.activateAll(t)

	// Drive ticks so the client drains replication messages and sends inputs.
	for i := 0; i < 30; i++ {
		env.tickManager()
		env.driveSimulation()
	}
	env.tickStats()

	snap := env.manager.Snapshot()
	if snap == nil {
		t.Fatal("nil snapshot")
	}
	if snap.TotalActivated == 0 {
		t.Error("TotalActivated=0 after activation")
	}
	if snap.TotalMessages == 0 {
		t.Error("TotalMessages=0 after replication ticks")
	}
	if snap.TotalBytes == 0 {
		t.Error("TotalBytes=0 after replication ticks")
	}
}

func TestSnapshotRatesReflectLastInterval(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount:  1,
		accounts:     []testAccount{{accountNumber: 1}},
		fastMovement: true,
	})
	defer env.cleanup()

	env.activateAll(t)

	// First stats tick — establishes baseline.
	env.tickStats()
	first := env.manager.Snapshot()

	// Drive more activity so messages are drained between the two stats ticks.
	for i := 0; i < 20; i++ {
		env.tickManager()
		env.driveSimulation()
	}

	// Wait until the lifetime total has grown (Hub and drain goroutine are async).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		env.tickStats()
		snap := env.manager.Snapshot()
		if snap.TotalMessages > first.TotalMessages {
			// The rate in this interval must also be non-zero.
			if snap.MessagesPerSecond == 0 {
				t.Error("MessagesPerSecond=0 despite TotalMessages growing")
			}
			if snap.BytesPerSecond == 0 {
				t.Error("BytesPerSecond=0 despite TotalBytes growing")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("TotalMessages did not increase after replication ticks; first=%d current=%d",
		first.TotalMessages, env.manager.Snapshot().TotalMessages)
}

func TestSnapshotImmutableAfterNextTick(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount:  1,
		accounts:     []testAccount{{accountNumber: 1}},
		fastMovement: true,
	})
	defer env.cleanup()

	env.activateAll(t)
	env.tickStats()
	first := env.manager.Snapshot()

	firstMessages := first.TotalMessages
	firstSampledAt := first.SampledAt

	// Drive more ticks and fire another stats tick.
	for i := 0; i < 20; i++ {
		env.tickManager()
		env.driveSimulation()
	}
	env.tickStats()

	// Original snapshot must not have changed.
	if first.TotalMessages != firstMessages {
		t.Errorf("snapshot mutated: TotalMessages changed from %d to %d", firstMessages, first.TotalMessages)
	}
	if first.SampledAt != firstSampledAt {
		t.Error("snapshot mutated: SampledAt changed")
	}

	// The new snapshot should differ.
	second := env.manager.Snapshot()
	if second == first {
		t.Error("expected a new snapshot pointer after second tick")
	}
}

func TestSnapshotDisconnectAndQueueFullCounted(t *testing.T) {
	var activeClient *Client
	env := startManagerTest(t, managerTestOptions{
		targetCount: 1,
		accounts:    []testAccount{{accountNumber: 1}},
		newClient: func(userID int64, username string) *Client {
			activeClient = NewClient(userID, username)
			return activeClient
		},
	})
	defer env.cleanup()

	env.activateAll(t)

	// Simulate a queue-full by having the client report it.
	activeClient.wasQueueFull.Store(true)
	activeClient.CloseSend()
	time.Sleep(10 * time.Millisecond)
	env.tickManager()
	env.tickStats()

	snap := env.manager.Snapshot()
	if snap == nil {
		t.Fatal("nil snapshot")
	}
	if snap.TotalDisconnects == 0 {
		t.Error("TotalDisconnects=0 after unexpected disconnect")
	}
	if snap.TotalQueueFull == 0 {
		t.Error("TotalQueueFull=0 after queue-full disconnect")
	}
	if snap.TotalFailed == 0 {
		t.Error("TotalFailed=0 after failure")
	}
}

func TestSnapshotConcurrentReads(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount: 2,
		accounts:    []testAccount{{accountNumber: 1}, {accountNumber: 2}},
	})
	defer env.cleanup()

	env.activateAll(t)
	env.tickStats()

	const readers = 8
	var wg sync.WaitGroup
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			snap := env.manager.Snapshot()
			if snap == nil {
				return
			}
			_ = snap.ActiveCount
			_ = snap.TotalMessages
		}()
	}
	wg.Wait()
}
