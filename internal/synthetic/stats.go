package synthetic

import "time"

// SyntheticSnapshot is an immutable point-in-time aggregate published once per
// second by the Manager. Reads are safe from any goroutine.
type SyntheticSnapshot struct {
	// Gauges
	TargetCount    int    `json:"targetCount"`
	Provisioning   int    `json:"provisioning"`
	Provisioned    int    `json:"provisioned"`
	ActivatingCount int   `json:"activatingCount"`
	ActiveCount    int    `json:"activeCount"`
	MovingCount    int    `json:"movingCount"`
	IdleCount      int    `json:"idleCount"`
	FailedCount    int    `json:"failedCount"`
	QueueHighWater uint32 `json:"queueHighWater"`

	// Rates over the last completed one-second interval
	InputsPerSecond      uint64 `json:"inputsPerSecond"`
	MessagesPerSecond    uint64 `json:"messagesPerSecond"`
	BytesPerSecond       uint64 `json:"bytesPerSecond"`
	DisconnectsPerSecond uint64 `json:"disconnectsPerSecond"`
	QueueFullPerSecond   uint64 `json:"queueFullPerSecond"`

	// Lifetime totals
	TotalActivated   uint64 `json:"totalActivated"`
	TotalFailed      uint64 `json:"totalFailed"`
	TotalDisconnects uint64 `json:"totalDisconnects"`
	TotalQueueFull   uint64 `json:"totalQueueFull"`
	TotalInputs      uint64 `json:"totalInputs"`
	TotalMessages    uint64 `json:"totalMessages"`
	TotalBytes       uint64 `json:"totalBytes"`

	SampledAt time.Time `json:"sampledAt"`
}
