package realtime

import (
	"sync"
	"sync/atomic"

	"map-walker/internal/game"
)

// replicationJob 是从 Hub actor 传递到 dispatcher worker 的不可变复制任务。
// 所有切片字段已深拷贝，worker 不会与 broadcastReplication 共享底层数组。
type replicationJob struct {
	recipientID int64
	tick        uint64
	client      ClientSender
	changes     ReplicationChanges
}

// copyReplicationChanges 深拷贝 ReplicationChanges 中所有切片字段和指针。
// 元素结构体均为纯值类型（无嵌套指针/切片/map），浅拷贝元素即安全。
func copyReplicationChanges(src ReplicationChanges) ReplicationChanges {
	dst := ReplicationChanges{}

	if src.SelfPosition != nil {
		cp := *src.SelfPosition
		dst.SelfPosition = &cp
	}

	if src.Entered != nil {
		dst.Entered = make([]game.PlayerState, len(src.Entered))
		copy(dst.Entered, src.Entered)
	}

	if src.LeftPlayerIDs != nil {
		dst.LeftPlayerIDs = make([]int64, len(src.LeftPlayerIDs))
		copy(dst.LeftPlayerIDs, src.LeftPlayerIDs)
	}

	if src.Positions != nil {
		dst.Positions = make([]game.PlayerPosition, len(src.Positions))
		copy(dst.Positions, src.Positions)
	}

	if src.Appearances != nil {
		dst.Appearances = make([]PlayerAppearanceUpdate, len(src.Appearances))
		copy(dst.Appearances, src.Appearances)
	}

	if src.CollectiblesEntered != nil {
		dst.CollectiblesEntered = make([]CollectibleEnteredItem, len(src.CollectiblesEntered))
		copy(dst.CollectiblesEntered, src.CollectiblesEntered)
	}

	if src.CollectibleIDsLeft != nil {
		dst.CollectibleIDsLeft = make([]uint64, len(src.CollectibleIDsLeft))
		copy(dst.CollectibleIDsLeft, src.CollectibleIDsLeft)
	}

	if src.CollectiblesSpawned != nil {
		dst.CollectiblesSpawned = make([]CollectibleSpawnedItem, len(src.CollectiblesSpawned))
		copy(dst.CollectiblesSpawned, src.CollectiblesSpawned)
	}

	if src.CollectibleIDsCollected != nil {
		dst.CollectibleIDsCollected = make([]uint64, len(src.CollectibleIDsCollected))
		copy(dst.CollectibleIDsCollected, src.CollectibleIDsCollected)
	}

	return dst
}

// ReplicationDispatcher 将 per-recipient 编码/发送工作从 Hub actor 卸载到 worker goroutine。
// 按 recipientID 分区，每 recipient 由一个固定 worker 处理，保证 per-recipient 顺序。
type ReplicationDispatcher struct {
	workers     []chan replicationJob
	workerCount int
	stopped     atomic.Bool

	Submitted    atomic.Int64
	Dropped      atomic.Int64
	Encoded      atomic.Int64
	SkippedEmpty atomic.Int64
	EncodeErrors atomic.Int64
	SendFailures atomic.Int64
	EncodedBytes atomic.Int64
	inFlight     atomic.Int64

	onSendFailure func(recipientID int64)
	wg            sync.WaitGroup
}

// NewReplicationDispatcher 创建 dispatcher 并启动 workerCount 个 worker goroutine。
// queueSize 是每个 worker 的有界队列容量。
// onSendFailure 在 Send 失败时回调，可为 nil。
func NewReplicationDispatcher(workerCount, queueSize int, onSendFailure func(int64)) *ReplicationDispatcher {
	d := &ReplicationDispatcher{
		workers:       make([]chan replicationJob, workerCount),
		workerCount:   workerCount,
		onSendFailure: onSendFailure,
	}
	for i := 0; i < workerCount; i++ {
		d.workers[i] = make(chan replicationJob, queueSize)
		d.wg.Add(1)
		go d.runWorker(d.workers[i])
	}
	return d
}

func (d *ReplicationDispatcher) runWorker(jobs chan replicationJob) {
	defer d.wg.Done()
	for job := range jobs {
		data, ok, err := TryEncodeReplicationUpdate(job.tick, job.recipientID, job.changes)
		if err != nil {
			d.EncodeErrors.Add(1)
			continue
		}
		if !ok {
			d.SkippedEmpty.Add(1)
			continue
		}
		d.Encoded.Add(1)
		d.EncodedBytes.Add(int64(len(data)))
		if !job.client.Send(data) {
			d.SendFailures.Add(1)
			if d.onSendFailure != nil {
				d.onSendFailure(job.recipientID)
			}
		}
		d.inFlight.Add(-1)
	}
}

// Submit 提交一个复制任务。若目标 worker 队列满，立即返回 false 并计数 drop。
// Submit 和 Stop 存在并发调用竞态：Hub 生命周期中 Submit 和 Stop 不会并发，关闭
// dispatcher 前必须确保无新的 Submit 调用。
func (d *ReplicationDispatcher) Submit(job replicationJob) bool {
	if d.stopped.Load() {
		return false
	}
	d.Submitted.Add(1)
	d.inFlight.Add(1)
	idx := int(job.recipientID) % d.workerCount
	if idx < 0 {
		idx = -idx
	}
	select {
	case d.workers[idx] <- job:
		return true
	default:
		d.Dropped.Add(1)
		d.inFlight.Add(-1)
		return false
	}
}

// Stop 关闭所有 worker 队列并等待 worker 退出。幂等：多次调用安全。
func (d *ReplicationDispatcher) Stop() {
	if d.stopped.Swap(true) {
		return
	}
	for _, ch := range d.workers {
		close(ch)
	}
	d.wg.Wait()
}

// WaitIdle 忙等待直到所有已提交的 job 处理完毕。仅用于 benchmark/test 同步。
func (d *ReplicationDispatcher) WaitIdle() {
	for d.inFlight.Load() > 0 {
	}
}

// Stats 返回当前统计快照。可从任意 goroutine 安全调用。
func (d *ReplicationDispatcher) Stats() DispatcherStats {
	var queueDepth int
	for i := range d.workerCount {
		queueDepth += len(d.workers[i])
	}
	return DispatcherStats{
		Submitted:    uint64(d.Submitted.Load()),
		Dropped:      uint64(d.Dropped.Load()),
		Encoded:      uint64(d.Encoded.Load()),
		SkippedEmpty: uint64(d.SkippedEmpty.Load()),
		EncodeErrors: uint64(d.EncodeErrors.Load()),
		SendFailures: uint64(d.SendFailures.Load()),
		EncodedBytes: uint64(d.EncodedBytes.Load()),
		QueueDepth:   queueDepth,
		WorkerCount:  d.workerCount,
	}
}
