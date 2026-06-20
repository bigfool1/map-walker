package realtime

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"map-walker/internal/game"
)

func TestReplicationDispatcherNonEmptyJobSends(t *testing.T) {
	d := NewReplicationDispatcher(1, 8, nil)
	defer d.Stop()

	tc := NewTestClient(1001, 8)

	// position ID 必须不同于 recipientID，否则 NormalizeReplicationChanges 会过滤掉
	changes := ReplicationChanges{
		Positions: []game.PlayerPosition{{ID: 2001, Lat: 31.1, Lng: 121.1}},
	}
	ok := d.Submit(replicationJob{recipientID: 1001, tick: 42, client: tc, changes: changes})
	if !ok {
		t.Fatal("submit should succeed")
	}

	msg := mustReceiveReplicationUpdate(t, tc)
	if msg.Tick != 42 {
		t.Fatalf("tick = %d, want 42", msg.Tick)
	}
	if len(msg.Positions) != 1 {
		t.Fatalf("positions = %d, want 1", len(msg.Positions))
	}

	if d.Encoded.Load() != 1 {
		t.Fatalf("encoded = %d, want 1", d.Encoded.Load())
	}
}

func TestReplicationDispatcherEmptyJobSkipped(t *testing.T) {
	d := NewReplicationDispatcher(1, 8, nil)
	defer d.Stop()

	tc := NewTestClient(1001, 8)

	// 空 changes — 应被跳过，不发送
	ok := d.Submit(replicationJob{recipientID: 1001, tick: 1, client: tc, changes: ReplicationChanges{}})
	if !ok {
		t.Fatal("submit should succeed")
	}

	// sentinel: 非空 job 确保空 job 已处理
	changes := ReplicationChanges{Positions: []game.PlayerPosition{{ID: 1, Lat: 31.1, Lng: 121.1}}}
	d.Submit(replicationJob{recipientID: 1001, tick: 2, client: tc, changes: changes})
	msg := mustReceiveReplicationUpdate(t, tc)
	if msg.Tick != 2 {
		t.Fatalf("tick = %d, want 2", msg.Tick)
	}

	if d.SkippedEmpty.Load() != 1 {
		t.Fatalf("skipped = %d, want 1", d.SkippedEmpty.Load())
	}
}

func TestReplicationDispatcherSendFailureCallback(t *testing.T) {
	var mu sync.Mutex
	var failedIDs []int64
	onFail := func(recipientID int64) {
		mu.Lock()
		failedIDs = append(failedIDs, recipientID)
		mu.Unlock()
	}

	d := NewReplicationDispatcher(1, 8, onFail)
	defer d.Stop()

	// Send 永远失败的 client
	failClient := &failClient{id: 2001}

	changes := ReplicationChanges{Positions: []game.PlayerPosition{{ID: 1, Lat: 31.1, Lng: 121.1}}}
	ok := d.Submit(replicationJob{recipientID: 2001, tick: 1, client: failClient, changes: changes})
	if !ok {
		t.Fatal("submit should succeed")
	}

	// sentinel 确保 fail job 已处理
	tc := NewTestClient(2001, 1)
	d.Submit(replicationJob{recipientID: 2001, tick: 2, client: tc, changes: changes})
	mustReceiveReplicationUpdate(t, tc)

	mu.Lock()
	n := len(failedIDs)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("callback count = %d, want 1, ids=%v", n, failedIDs)
	}
	if d.SendFailures.Load() != 1 {
		t.Fatalf("sendFailures = %d, want 1", d.SendFailures.Load())
	}
}

func TestReplicationDispatcherFullQueueDrop(t *testing.T) {
	d := NewReplicationDispatcher(1, 1, nil)
	// 不调 Stop — worker 会永久阻塞在 hangClient.Send

	hang := &hangClient{id: 3001}
	changes := ReplicationChanges{Positions: []game.PlayerPosition{{ID: 1, Lat: 31.1, Lng: 121.1}}}

	// job 1: worker 取走并阻塞在 Send
	d.Submit(replicationJob{recipientID: 3001, tick: 1, client: hang, changes: changes})
	time.Sleep(10 * time.Millisecond)

	// job 2: 填满队列（容量 1）
	ok := d.Submit(replicationJob{recipientID: 3001, tick: 2, client: hang, changes: changes})
	if !ok {
		t.Fatal("second submit should succeed (queue has space)")
	}

	// job 3: 队列满，提交失败
	ok = d.Submit(replicationJob{recipientID: 3001, tick: 3, client: hang, changes: changes})
	if ok {
		t.Fatal("third submit should fail (queue full)")
	}

	if d.Dropped.Load() != 1 {
		t.Fatalf("dropped = %d, want 1", d.Dropped.Load())
	}
}

func TestReplicationDispatcherOrderPreservation(t *testing.T) {
	d := NewReplicationDispatcher(1, 8, nil)
	defer d.Stop()

	tc := NewTestClient(1001, 8)

	ch1 := ReplicationChanges{Positions: []game.PlayerPosition{{ID: 1, Lat: 31.1, Lng: 121.1}}}
	ch2 := ReplicationChanges{Positions: []game.PlayerPosition{{ID: 2, Lat: 31.2, Lng: 121.2}}}

	d.Submit(replicationJob{recipientID: 1001, tick: 1, client: tc, changes: ch1})
	d.Submit(replicationJob{recipientID: 1001, tick: 2, client: tc, changes: ch2})

	msg1 := mustReceiveReplicationUpdate(t, tc)
	msg2 := mustReceiveReplicationUpdate(t, tc)

	if len(msg1.Positions) != 1 || msg1.Positions[0].ID != 1 {
		t.Fatalf("first msg position ID = %v, want 1", msg1.Positions)
	}
	if len(msg2.Positions) != 1 || msg2.Positions[0].ID != 2 {
		t.Fatalf("second msg position ID = %v, want 2", msg2.Positions)
	}
}

func TestReplicationDispatcherStopIdempotent(t *testing.T) {
	d := NewReplicationDispatcher(2, 8, nil)
	d.Stop()
	d.Stop() // 不应 panic

	// Stop 后 Submit 返回 false
	if d.Submit(replicationJob{recipientID: 1001}) {
		t.Fatal("submit after stop should fail")
	}
}

// failClient: Send 永远返回 false
type failClient struct {
	id int64
}

func (c *failClient) ID() int64            { return c.id }
func (c *failClient) Username() string      { return "fail" }
func (c *failClient) Send([]byte) bool      { return false }
func (c *failClient) CloseSend()            {}

// hangClient: Send 永久阻塞
type hangClient struct {
	id      int64
	blocked atomic.Bool
}

func (c *hangClient) ID() int64       { return c.id }
func (c *hangClient) Username() string { return "hang" }
func (c *hangClient) Send([]byte) bool {
	c.blocked.Store(true)
	select {} // 永久阻塞
}
func (c *hangClient) CloseSend() {}
