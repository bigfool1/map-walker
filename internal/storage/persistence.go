package storage

import (
	"log"
	"sync"

	"map-walker/internal/realtime"
)

type PersistenceWorker struct {
	db       *DB
	save     func(realtime.PositionUpdate) error
	ordered  chan []realtime.PositionUpdate
	flush    chan chan struct{}
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	lastSeq  map[int64]uint64
}

func NewPersistenceWorker(db *DB) *PersistenceWorker {
	w := &PersistenceWorker{
		db: db,
		save: func(update realtime.PositionUpdate) error {
			return db.SaveUserPosition(update.UserID, update.Lat, update.Lng)
		},
		ordered: make(chan []realtime.PositionUpdate),
		flush:   make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		lastSeq: map[int64]uint64{},
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

// apply 过滤折叠后按存储后端路由写入策略。
func (w *PersistenceWorker) apply(batch []realtime.PositionUpdate) {
	accepted := w.filterAndCollapse(batch)
	if len(accepted) == 0 {
		return
	}

	if w.db != nil && w.db.Driver() == "mysql" {
		w.applyBulk(accepted)
		return
	}
	w.applyPerRow(accepted)
}

// filterAndCollapse 过滤 lastSeq 过期的更新，同一用户只保留最高 seq。
func (w *PersistenceWorker) filterAndCollapse(batch []realtime.PositionUpdate) []realtime.PositionUpdate {
	best := make(map[int64]realtime.PositionUpdate, len(batch))
	for _, u := range batch {
		if u.Seq <= w.lastSeq[u.UserID] {
			continue
		}
		if existing, ok := best[u.UserID]; !ok || u.Seq > existing.Seq {
			best[u.UserID] = u
		}
	}
	result := make([]realtime.PositionUpdate, 0, len(best))
	for _, u := range best {
		result = append(result, u)
	}
	return result
}

// applyPerRow SQLite 路径：逐行保存，保留现有行为和错误格式。
func (w *PersistenceWorker) applyPerRow(updates []realtime.PositionUpdate) {
	for _, u := range updates {
		if err := w.save(u); err != nil {
			log.Printf("persist position user=%d: %v", u.UserID, err)
			continue
		}
		w.lastSeq[u.UserID] = u.Seq
	}
}

// applyBulk MySQL 路径：按 MaxPositionChunkSize 分块批量保存。
// 每块独立事务，失败块不阻塞后续块，只推进成功块的 lastSeq。
func (w *PersistenceWorker) applyBulk(updates []realtime.PositionUpdate) {
	for i := 0; i < len(updates); i += MaxPositionChunkSize {
		end := min(i+MaxPositionChunkSize, len(updates))
		chunk := updates[i:end]

		if err := w.db.SavePositionChunk(chunk); err != nil {
			log.Printf("persist position chunk (offset=%d size=%d): %v", i, len(chunk), err)
			continue
		}
		for _, u := range chunk {
			w.lastSeq[u.UserID] = u.Seq
		}
	}
}
