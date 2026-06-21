package realtime

import "time"

// DispatcherStats 是 ReplicationDispatcher 当前时刻的统计快照。
type DispatcherStats struct {
	Submitted    uint64
	Dropped      uint64
	Encoded      uint64
	SkippedEmpty uint64
	EncodeErrors uint64
	SendFailures uint64
	EncodedBytes uint64
	QueueDepth   int
	WorkerCount  int
}

// BuilderStats 是 ReplicationBuilder 单次 Build 调用的指标快照。
type BuilderStats struct {
	Recipients           int           // 触达的不同接收者数
	Jobs                 int           // 产出的 job 数
	AccumulationDuration time.Duration // 按接收者累积耗时
	CopyDuration         time.Duration // 深拷贝耗时
	TotalDuration        time.Duration // 总耗时
}

// HubSnapshot is an immutable point-in-time aggregate of the most recently
// completed one-second Hub stats interval. Reads are safe from any goroutine.
type HubSnapshot struct {
	ConnectedClients      int
	AcceptedInputs        uint64
	SimulationTicks       uint64
	MovedPlayers          uint64
	AOICandidatePairs     uint64
	AOIDistanceChecks     uint64
	RelationshipsEntered  uint64
	RelationshipsLeft     uint64
	ReplicationMessages   uint64
	ReplicationRecipients uint64
	ReplicationBytes      uint64
	Builder               BuilderStats
	Dispatcher            DispatcherStats
	AOIDetailedMoveDuration   time.Duration
	CollectibleRecalcDuration time.Duration
	SampledAt             time.Time
}
