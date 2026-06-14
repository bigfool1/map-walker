package aoiworkload

import (
	"fmt"

	"map-walker/internal/game"
)

const (
	TotalAOIUpdates    = 120
	WarmupAOIUpdates   = 20
	MeasuredAOIUpdates = 100

	SimTickIntervalMs        = 50
	AOIUpdatesPerSimTickPair = 1
)

type Density string

const (
	DensitySparse  Density = "sparse"
	DensityNormal  Density = "normal"
	DensityHotspot Density = "hotspot"
)

var (
	FrozenScales      = []int{100_000, 500_000, 1_000_000}
	FrozenMoverCounts = []int{10_000, 50_000}
	FrozenDensities   = []Density{DensitySparse, DensityNormal, DensityHotspot}
)

type Config struct {
	Scale      int
	MoverCount int
	Density    Density
	Seed       int64
}

func (c Config) IsApplicable() bool {
	return c.MoverCount > 0 && c.MoverCount <= c.Scale
}

func (c Config) String() string {
	return fmt.Sprintf("%d/%d/%s/seed=%d", c.Scale, c.MoverCount, c.Density, c.Seed)
}

type MovementPattern string

const (
	MovementCellLocal    MovementPattern = "cell_local"
	MovementCellCrossing MovementPattern = "cell_crossing"
)

type PlayerPlacement struct {
	ID  string
	Lat float64
	Lng float64
}

type DensitySample struct {
	Mean float64
	Min  float64
	Max  float64
}

type VisibilityChurnStats struct {
	Mean float64
	P50  float64
	P95  float64
	Max  int
}

type Scenario struct {
	Config Config

	WorldConfig game.Config
	AOIConfig   game.AOIConfig

	Players      []PlayerPlacement
	BuildOrder   []int
	MoverIndices []int
	HotspotCount int

	Schedule CompactMovementSchedule

	InitialDensity     DensitySample
	SteadyStateDensity DensitySample
	VisibilityChurn    VisibilityChurnStats
}

func DefaultWorldConfig() game.Config {
	return game.DefaultConfig()
}

func AllFrozenConfigs(seed int64) []Config {
	configs := make([]Config, 0)
	for _, scale := range FrozenScales {
		for _, movers := range FrozenMoverCounts {
			for _, density := range FrozenDensities {
				configs = append(configs, Config{
					Scale:      scale,
					MoverCount: movers,
					Density:    density,
					Seed:       seed,
				})
			}
		}
	}
	return configs
}

func BaselineMatrixConfigs(seed int64) []Config {
	return []Config{
		{Scale: 100_000, MoverCount: 10_000, Density: DensityNormal, Seed: seed},
		{Scale: 1_000_000, MoverCount: 10_000, Density: DensityNormal, Seed: seed},
		{Scale: 1_000_000, MoverCount: 50_000, Density: DensitySparse, Seed: seed},
		{Scale: 1_000_000, MoverCount: 50_000, Density: DensityNormal, Seed: seed},
		{Scale: 1_000_000, MoverCount: 50_000, Density: DensityHotspot, Seed: seed},
	}
}
