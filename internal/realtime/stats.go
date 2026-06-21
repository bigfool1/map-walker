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
	Dispatcher            DispatcherStats
	SampledAt             time.Time
}
