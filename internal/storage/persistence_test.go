package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"map-walker/internal/realtime"
)

// saveRecorder 记录 save 调用，供测试断言用。并发安全。
type saveRecorder struct {
	mu     sync.Mutex
	calls  []realtime.PositionUpdate
	failed map[int64]int // userID -> 失败次数
}

func (r *saveRecorder) record(u realtime.PositionUpdate) {
	r.mu.Lock()
	r.calls = append(r.calls, u)
	r.mu.Unlock()
}

func (r *saveRecorder) saved() []realtime.PositionUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]realtime.PositionUpdate, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *saveRecorder) failNext(userID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed == nil {
		r.failed = map[int64]int{}
	}
	if r.failed[userID] == 0 {
		r.failed[userID]++
		return true
	}
	return false
}

func TestPersistenceWorkerSavesBatch(t *testing.T) {
	db := openTestDB(t)
	id := createTestUser(t, db)

	worker := NewPersistenceWorker(db)
	worker.Submit([]realtime.PositionUpdate{{
		UserID: id,
		Lat:    31.1,
		Lng:    121.1,
		Seq:    1,
	}})
	worker.Drain()

	lat, lng, ok, err := db.GetUserPosition(id)
	if err != nil || !ok {
		t.Fatalf("get position failed: ok=%v err=%v", ok, err)
	}
	if lat != 31.1 || lng != 121.1 {
		t.Fatalf("unexpected position: %v %v", lat, lng)
	}
}

func TestPersistenceWorkerRejectsStaleSequence(t *testing.T) {
	db := openTestDB(t)
	id := createTestUser(t, db)

	worker := NewPersistenceWorker(db)
	worker.Submit([]realtime.PositionUpdate{{
		UserID: id,
		Lat:    31.2,
		Lng:    121.2,
		Seq:    2,
	}})
	worker.Submit([]realtime.PositionUpdate{{
		UserID: id,
		Lat:    31.0,
		Lng:    121.0,
		Seq:    1,
	}})
	worker.Drain()

	lat, lng, ok, err := db.GetUserPosition(id)
	if err != nil || !ok {
		t.Fatalf("get position failed: ok=%v err=%v", ok, err)
	}
	if lat != 31.2 || lng != 121.2 {
		t.Fatalf("stale save overwrote newer position: %v %v", lat, lng)
	}
}

func TestPersistenceWorkerDrainProcessesQueuedWork(t *testing.T) {
	db := openTestDB(t)
	id := createTestUser(t, db)

	block := make(chan struct{})
	worker := &PersistenceWorker{
		save: func(update realtime.PositionUpdate) error {
			<-block
			return db.SaveUserPosition(update.UserID, update.Lat, update.Lng)
		},
		ordered: make(chan []realtime.PositionUpdate),
		flush:   make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		lastSeq: map[int64]uint64{},
	}
	go worker.run()

	worker.ordered <- []realtime.PositionUpdate{{
		UserID: id,
		Lat:    31.3,
		Lng:    121.3,
		Seq:    1,
	}}

	done := make(chan struct{})
	go func() {
		worker.Drain()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("drain returned before save completed")
	case <-time.After(20 * time.Millisecond):
	}

	close(block)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain did not wait for blocked save")
	}
}

// TestPersistenceWorkerSameBatchCollapsesToHighestSeq 同一批次同一用户多次更新时，
// 只有最高 seq 的被保存（由 filterAndCollapse 预折叠）。
//
// 用同步 channel send 而非 Submit 入队，避免 Submit 异步 goroutine 与 Drain 的竞态窗口
// （见 docs/concurrency-debugging.md 陷阱 6）。
func TestPersistenceWorkerSameBatchCollapsesToHighestSeq(t *testing.T) {
	rec := &saveRecorder{}
	worker := &PersistenceWorker{
		save: func(u realtime.PositionUpdate) error {
			rec.record(u)
			return nil
		},
		ordered: make(chan []realtime.PositionUpdate),
		flush:   make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		lastSeq: map[int64]uint64{},
	}
	go worker.run()
	defer worker.Stop()

	worker.ordered <- []realtime.PositionUpdate{
		{UserID: 1, Lat: 30.0, Lng: 120.0, Seq: 2},
		{UserID: 1, Lat: 31.0, Lng: 121.0, Seq: 5},
		{UserID: 1, Lat: 32.0, Lng: 122.0, Seq: 3},
	}
	worker.Drain()

	saved := rec.saved()
	if len(saved) != 1 {
		t.Fatalf("同一用户应只保存最高 seq，实际保存 %d 条: %+v", len(saved), saved)
	}
	if saved[0].Seq != 5 {
		t.Fatalf("应保存 seq=5，实际保存 seq=%d", saved[0].Seq)
	}
}

// TestPersistenceWorkerFailedSaveDoesNotAdvanceLastSeq 保存失败不推进 lastSeq，
// 同一 seq 可在后续批次中重试。
func TestPersistenceWorkerFailedSaveDoesNotAdvanceLastSeq(t *testing.T) {
	rec := &saveRecorder{}
	worker := &PersistenceWorker{
		save: func(u realtime.PositionUpdate) error {
			if rec.failNext(u.UserID) {
				return fmt.Errorf("injected error")
			}
			rec.record(u)
			return nil
		},
		ordered: make(chan []realtime.PositionUpdate),
		flush:   make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		lastSeq: map[int64]uint64{},
	}
	go worker.run()
	defer worker.Stop()

	// 第一次提交：保存失败
	worker.ordered <- []realtime.PositionUpdate{
		{UserID: 1, Lat: 30.0, Lng: 120.0, Seq: 1},
	}
	worker.Drain()

	// 第二次提交：同 seq，应被重试
	worker.ordered <- []realtime.PositionUpdate{
		{UserID: 1, Lat: 30.0, Lng: 120.0, Seq: 1},
	}
	worker.Drain()

	saved := rec.saved()
	if len(saved) != 1 {
		t.Fatalf("失败后重试应保存 1 条，实际 %d 条: %+v", len(saved), saved)
	}
	if saved[0].Seq != 1 {
		t.Fatalf("应保存 seq=1，实际 seq=%d", saved[0].Seq)
	}
}

// TestPersistenceWorkerFailureIsolationAcrossBatches 一批失败不影响后续批次。
func TestPersistenceWorkerFailureIsolationAcrossBatches(t *testing.T) {
	rec := &saveRecorder{}
	worker := &PersistenceWorker{
		save: func(u realtime.PositionUpdate) error {
			if u.UserID == 1 && rec.failNext(u.UserID) {
				return fmt.Errorf("injected error for user %d", u.UserID)
			}
			rec.record(u)
			return nil
		},
		ordered: make(chan []realtime.PositionUpdate),
		flush:   make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		lastSeq: map[int64]uint64{},
	}
	go worker.run()
	defer worker.Stop()

	// 批次 1：user=1 的保存失败
	worker.ordered <- []realtime.PositionUpdate{
		{UserID: 1, Lat: 30.0, Lng: 120.0, Seq: 1},
	}
	worker.Drain()

	// 批次 2：应正常保存
	worker.ordered <- []realtime.PositionUpdate{
		{UserID: 2, Lat: 31.0, Lng: 121.0, Seq: 1},
	}
	worker.Drain()

	saved := rec.saved()
	if len(saved) != 1 {
		t.Fatalf("预期批次 2 保存 1 条，实际 %d 条: %+v", len(saved), saved)
	}
	if saved[0].UserID != 2 {
		t.Fatalf("预期保存 user=2，实际 user=%d", saved[0].UserID)
	}
}

func createTestUser(t *testing.T, db *DB) int64 {
	t.Helper()
	id, err := db.CreateUser(User{
		Username:           "testuser",
		UsernameNormalized: "testuser",
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	return id
}
