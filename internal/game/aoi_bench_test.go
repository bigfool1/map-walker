package game

import (
	"math/rand"
	"testing"
)

const benchPlayerCount = 2000

func newBenchAOI(b *testing.B) (*AOIIndex, []int64) {
	b.Helper()
	cfg := AOIConfigFromWorld(testConfig())
	aoi := NewAOIIndex(cfg)
	ids := make([]int64, 0, benchPlayerCount)
	rng := rand.New(rand.NewSource(1))
	// 将玩家均匀分布在原点为中心的 6km × 6km 区域内
	const span = 6000.0
	for i := 0; i < benchPlayerCount; i++ {
		x := (rng.Float64() - 0.5) * span
		y := (rng.Float64() - 0.5) * span
		lat, lng := cfg.LocalToLatLng(x, y)
		id := int64(i + 1)
		aoi.Insert(id, lat, lng)
		ids = append(ids, id)
	}
	aoi.TakeStats()
	return aoi, ids
}

func BenchmarkAOIMoveSameCellSmall(b *testing.B) {
	aoi, ids := newBenchAOI(b)
	cfg := aoi.config
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%len(ids)]
		px, py, _ := aoi.LocalPosition(id)
		// 向东移 5m，远低于 50m 阈值且远在 cell 内
		lat, lng := cfg.LocalToLatLng(px+5, py)
		aoi.MoveDetailed(id, lat, lng)
	}
	b.ReportMetric(float64(aoi.TakeStats().SkippedEnterScans)/float64(b.N), "skipped/op")
}

func BenchmarkAOIMoveBeyondThreshold(b *testing.B) {
	aoi, ids := newBenchAOI(b)
	cfg := aoi.config
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%len(ids)]
		px, py, _ := aoi.LocalPosition(id)
		// 向东移 60m，超过阈值但通常同 cell
		lat, lng := cfg.LocalToLatLng(px+60, py)
		aoi.MoveDetailed(id, lat, lng)
	}
	b.ReportMetric(float64(aoi.TakeStats().FullEnterScans)/float64(b.N), "full_scans/op")
}

func BenchmarkAOIMoveCrossCell(b *testing.B) {
	aoi, ids := newBenchAOI(b)
	cfg := aoi.config
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%len(ids)]
		px, py, _ := aoi.LocalPosition(id)
		// 向东移 700m，保证换 cell
		lat, lng := cfg.LocalToLatLng(px+700, py)
		aoi.MoveDetailed(id, lat, lng)
	}
	b.ReportMetric(float64(aoi.TakeStats().FullEnterScans)/float64(b.N), "full_scans/op")
}
