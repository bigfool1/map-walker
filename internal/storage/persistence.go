package storage

import (
	"log"
	"sync"

	"map-walker/internal/realtime"
)

type PersistenceWorker struct {
	save    func(realtime.PositionUpdate) error
	ordered chan []realtime.PositionUpdate
	flush   chan chan struct{}
	stop    chan struct{}
	done    chan struct{}
	stopOnce sync.Once
	lastSeq map[string]uint64
}

func NewPersistenceWorker(db *DB) *PersistenceWorker {
	w := &PersistenceWorker{
		save: func(update realtime.PositionUpdate) error {
			return db.SaveUserPosition(update.UserID, update.Lat, update.Lng)
		},
		ordered: make(chan []realtime.PositionUpdate),
		flush:   make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		lastSeq: map[string]uint64{},
	}
	go w.run()
	return w
}

func (w *PersistenceWorker) Submit(updates []realtime.PositionUpdate) {
	if len(updates) == 0 {
		return
	}
	batch := append([]realtime.PositionUpdate(nil), updates...)
	go func() {
		w.ordered <- batch
	}()
}

// SubmitSync 同步发送位置更新；调用者阻塞直到 worker 接收并写入 DB。
// 用于 final save 这类必须保证已提交的场景，不用于周期性持久化。
func (w *PersistenceWorker) SubmitSync(updates []realtime.PositionUpdate) {
	if len(updates) == 0 {
		return
	}
	batch := append([]realtime.PositionUpdate(nil), updates...)
	w.ordered <- batch
}

func (w *PersistenceWorker) Drain() {
	ack := make(chan struct{})
	select {
	case w.flush <- ack:
		<-ack
	case <-w.stop:
	}
}

func (w *PersistenceWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stop)
	})
	<-w.done
}

func (w *PersistenceWorker) run() {
	defer close(w.done)

	for {
		select {
		case batch := <-w.ordered:
			w.apply(batch)
		case ack := <-w.flush:
			w.drainPending()
			ack <- struct{}{}
		case <-w.stop:
			w.drainPending()
			return
		}
	}
}

func (w *PersistenceWorker) drainPending() {
	for {
		select {
		case batch := <-w.ordered:
			w.apply(batch)
		default:
			return
		}
	}
}

func (w *PersistenceWorker) apply(batch []realtime.PositionUpdate) {
	for _, update := range batch {
		if update.Seq <= w.lastSeq[update.UserID] {
			continue
		}
		if err := w.save(update); err != nil {
			log.Printf("persist position user=%s: %v", update.UserID, err)
			continue
		}
		w.lastSeq[update.UserID] = update.Seq
	}
}
