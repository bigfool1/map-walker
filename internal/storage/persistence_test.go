package storage

import (
	"testing"
	"time"

	"map-walker/internal/realtime"
)

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
