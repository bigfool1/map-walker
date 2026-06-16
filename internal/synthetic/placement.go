package synthetic

import (
	"math"

	"map-walker/internal/game"
)

const activityHalfExtentMeters = 1500

type PlacementConfig struct {
	SpawnLat float64
	SpawnLng float64
}

func DefaultPlacementConfig() PlacementConfig {
	cfg := game.DefaultConfig()
	return PlacementConfig{
		SpawnLat: cfg.SpawnLat,
		SpawnLng: cfg.SpawnLng,
	}
}

func PlacementLatLng(cfg PlacementConfig, accountNumber int) (lat, lng float64) {
	localX, localY := localPlacement(accountNumber)
	aoiCfg := game.AOIConfigFromWorld(game.Config{
		SpawnLat: cfg.SpawnLat,
		SpawnLng: cfg.SpawnLng,
	})
	return aoiCfg.LocalToLatLng(localX, localY)
}

func localPlacement(accountNumber int) (localX, localY float64) {
	fx := placementFraction(accountNumber, 1)
	fy := placementFraction(accountNumber, 2)
	localX = fx*2*activityHalfExtentMeters - activityHalfExtentMeters
	localY = fy*2*activityHalfExtentMeters - activityHalfExtentMeters
	return localX, localY
}

func placementFraction(accountNumber, salt int) float64 {
	v := uint64(accountNumber)*0x9E3779B97F4A7C15 + uint64(salt)*0xBF58476D1CE4E5B9
	v ^= v >> 33
	v *= 0xff51afd7ed558ccd
	v ^= v >> 33
	return float64(v%10000) / 10000
}

func LocalPlacementDistance(accountNumberA, accountNumberB int) float64 {
	ax, ay := localPlacement(accountNumberA)
	bx, by := localPlacement(accountNumberB)
	return math.Hypot(ax-bx, ay-by)
}

func WithinActivityRegion(accountNumber int) bool {
	localX, localY := localPlacement(accountNumber)
	return math.Abs(localX) <= activityHalfExtentMeters && math.Abs(localY) <= activityHalfExtentMeters
}
