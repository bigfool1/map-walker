package synthetic

import "time"

// SyntheticSnapshot is an immutable point-in-time aggregate published once per
// second by the Manager. Reads are safe from any goroutine.
type SyntheticSnapshot struct {
	// Gauges
	Target         int
	Provisioning   int // slots waiting for auto-provision result
	Provisioned    int // slots with identity, waiting for ramp-up
	Activating     int
	Active         int
	Moving         int    // active clients with a non-neutral input
	Idle           int    // active clients with neutral input
	Failed         int
	QueueHighWater uint32 // max seen across all clients, including removed ones

	// Rates over the last completed one-second interval
	InputsRate      uint64
	MessagesRate    uint64
	BytesRate       uint64
	DisconnectsRate uint64
	QueueFullRate   uint64

	// Lifetime totals
	TotalActivated   uint64
	TotalFailed      uint64
	TotalDisconnects uint64
	TotalQueueFull   uint64
	TotalInputs      uint64
	TotalMessages    uint64
	TotalBytes       uint64

	SampledAt time.Time
}
