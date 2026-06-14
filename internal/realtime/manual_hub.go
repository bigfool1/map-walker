package realtime

import (
	"time"

	"map-walker/internal/game"
)

// NewManualTickHub creates a hub with externally driven tick channels for tests.
func NewManualTickHub(config game.Config, loader SavedPlayerLoader, persister PositionPersister) (*Hub, chan time.Time, chan time.Time, chan time.Time, chan time.Time) {
	simulations := make(chan time.Time, 8)
	broadcasts := make(chan time.Time, 8)
	persistence := make(chan time.Time, 8)
	stats := make(chan time.Time, 8)
	world := game.NewWorld(config)
	hub := newHub(world, loader, persister, simulations, broadcasts, persistence, stats, func() {})
	return hub, simulations, broadcasts, persistence, stats
}
