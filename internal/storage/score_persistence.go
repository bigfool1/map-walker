package storage

import (
	"sync"
	"time"

	"map-walker/internal/realtime"
)

const (
	scoreInitialBackoff = 100 * time.Millisecond
	scoreMaxBackoff     = 30 * time.Second
)

type syncScoreRequest struct {
	userID int64
	score  int64
	done   chan struct{}
}

// ScorePersister 异步单调分数持久化 worker，合并重复提交，失败重试退避
// 零值不可用，必须通过 NewScorePersister 创建
type ScorePersister struct {
	db       *DB
	mu       sync.Mutex
	pending  map[int64]int64 // userID -> highestScore
	submitCh chan realtime.ScoreUpdate
	syncCh   chan syncScoreRequest
	drainCh  chan chan struct{}
	stopCh   chan struct{}
	stopped  chan struct{}
}

// NewScorePersister 创建并启动分数持久化 worker
func NewScorePersister(db *DB) *ScorePersister {
	p := &ScorePersister{
		db:       db,
		pending:  make(map[int64]int64),
		submitCh: make(chan realtime.ScoreUpdate, 64),
		syncCh:   make(chan syncScoreRequest),
		drainCh:  make(chan chan struct{}),
		stopCh:   make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	go p.run()
	return p
}

// Submit 异步提交分数快照（不阻塞 Hub）
func (p *ScorePersister) Submit(update realtime.ScoreUpdate) {
	select {
	case p.submitCh <- update:
	default:
	}
}

// SubmitSync 同步持久化单个用户的最新分数，用于 disconnect/logout
func (p *ScorePersister) SubmitSync(userID int64, score int64) {
	// 合并 pending 中更高的分数
	p.mu.Lock()
	if current, ok := p.pending[userID]; ok && current > score {
		score = current
	}
	delete(p.pending, userID)
	p.mu.Unlock()

	done := make(chan struct{})
	p.syncCh <- syncScoreRequest{userID: userID, score: score, done: done}
	<-done
}

// Drain 阻塞等待所有 pending 分数持久化完成
func (p *ScorePersister) Drain() {
	reply := make(chan struct{})
	p.drainCh <- reply
	<-reply
}

// Stop 通知 worker 退出（不等待）
func (p *ScorePersister) Stop() {
	close(p.stopCh)
	<-p.stopped
}

func (p *ScorePersister) run() {
	defer close(p.stopped)
	backoff := scoreInitialBackoff

	for {
		p.mu.Lock()
		hasPending := len(p.pending) > 0
		p.mu.Unlock()

		if hasPending {
			if p.flushPending() {
				backoff = scoreInitialBackoff
			} else {
				backoff = minDuration(backoff*2, scoreMaxBackoff)
			}
		}

		select {
		case u := <-p.submitCh:
			p.coalesce(u)
			// 清空 channel 中已就绪的提交，合并为最高分数
			p.drainSubmitCh()
		case req := <-p.syncCh:
			p.persistSync(req)
		case reply := <-p.drainCh:
			p.drainAll()
			close(reply)
		case <-p.stopCh:
			return
		case <-time.After(backoff):
			// 重试计时器到期，循环继续检查 pending
		}
	}
}

func (p *ScorePersister) coalesce(u realtime.ScoreUpdate) {
	p.mu.Lock()
	if current, ok := p.pending[u.UserID]; !ok || u.Score > current {
		p.pending[u.UserID] = u.Score
	}
	p.mu.Unlock()
}

// flushPending 尝试持久化所有 pending 分数，全部成功返回 true
func (p *ScorePersister) flushPending() bool {
	p.mu.Lock()
	snapshot := make(map[int64]int64, len(p.pending))
	for uid, score := range p.pending {
		snapshot[uid] = score
	}
	p.mu.Unlock()

	allOk := true
	for uid, score := range snapshot {
		if err := p.db.SaveScore(uid, score); err != nil {
			allOk = false
			continue
		}
		// 仅删除未被新提交覆盖的条目
		p.mu.Lock()
		if current, ok := p.pending[uid]; ok && current == score {
			delete(p.pending, uid)
		}
		p.mu.Unlock()
	}
	return allOk
}

// persistSync 同步持久化单个用户分数（带退避重试）
func (p *ScorePersister) persistSync(req syncScoreRequest) {
	backoff := scoreInitialBackoff
	for {
		err := p.db.SaveScore(req.userID, req.score)
		if err == nil {
			close(req.done)
			return
		}
		select {
		case <-time.After(backoff):
			backoff = minDuration(backoff*2, scoreMaxBackoff)
		case <-p.stopCh:
			close(req.done)
			return
		}
	}
}

// drainAll 同步持久化所有 pending，带退避重试直到全部成功
func (p *ScorePersister) drainAll() {
	backoff := scoreInitialBackoff
	for {
		p.mu.Lock()
		hasPending := len(p.pending) > 0
		p.mu.Unlock()

		if !hasPending {
			return
		}

		if p.flushPending() {
			return
		}

		select {
		case <-time.After(backoff):
			backoff = minDuration(backoff*2, scoreMaxBackoff)
		case <-p.stopCh:
			return
		}
	}
}

// drainSubmitCh 非阻塞清空 submitCh，合并所有已就绪的提交
func (p *ScorePersister) drainSubmitCh() {
	for {
		select {
		case u := <-p.submitCh:
			p.coalesce(u)
		default:
			return
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
