package game

import (
	"math/rand/v2"
	"sort"
	"testing"
	"time"
)

func testAOIConfig() AOIConfig {
	return AOIConfigFromWorld(testConfig())
}

func testRegions() []CollectibleRegion {
	return []CollectibleRegion{
		{
			ID: "region-1", CenterLat: 31.2304, CenterLng: 121.4737,
			RadiusMeters: 200, TargetCount: 5,
			RespawnMin: 5 * time.Second, RespawnMax: 10 * time.Second,
		},
		{
			ID: "region-2", CenterLat: 31.2350, CenterLng: 121.4780,
			RadiusMeters: 200, TargetCount: 5,
			RespawnMin: 3 * time.Second, RespawnMax: 8 * time.Second,
		},
		{
			ID: "region-3", CenterLat: 31.2270, CenterLng: 121.4700,
			RadiusMeters: 200, TargetCount: 5,
			RespawnMin: 5 * time.Second, RespawnMax: 15 * time.Second,
		},
	}
}

func newTestField(seed uint64) (*CollectibleField, func() time.Time) {
	var now time.Time
	timeNow := func() time.Time { return now }
	field := NewCollectibleField(testAOIConfig(), testRegions(), timeNow, rand.New(rand.NewPCG(seed, seed)))
	return field, func() time.Time { return now }
}

func newTestFieldWithTime(seed uint64, t0 time.Time) (*CollectibleField, *time.Time) {
	now := t0
	timeNow := func() time.Time { return now }
	field := NewCollectibleField(testAOIConfig(), testRegions(), timeNow, rand.New(rand.NewPCG(seed, seed)))
	return field, &now
}

func TestPopulateReachesTarget(t *testing.T) {
	field, _ := newTestField(42)
	created := field.Populate()

	if field.Count() != 15 {
		t.Fatalf("CollectibleField.Count() = %d, want 15 (3×5)", field.Count())
	}
	if len(created) != 15 {
		t.Fatalf("len(Populate()) = %d, want 15", len(created))
	}

	// 按区域统计
	counts := map[string]int{}
	for _, c := range created {
		counts[c.RegionID]++
	}
	for _, region := range testRegions() {
		if counts[region.ID] != region.TargetCount {
			t.Fatalf("区域 %s 收集品数 = %d, want %d", region.ID, counts[region.ID], region.TargetCount)
		}
	}
}

func TestPopulateIDMonotonic(t *testing.T) {
	field, _ := newTestField(42)
	created := field.Populate()

	for i, c := range created {
		if c.ID != uint64(i+1) {
			t.Fatalf("created[%d].ID = %d, want %d", i, c.ID, i+1)
		}
	}
}

func TestAllCollectiblesInsideRegions(t *testing.T) {
	field, _ := newTestField(42)
	created := field.Populate()

	for _, c := range created {
		region, ok := field.RegionByID(c.RegionID)
		if !ok {
			t.Fatalf("找不到区域 %s", c.RegionID)
		}
		// 验证收集品在圆形区域内
		dist := haversineMeters(region.CenterLat, region.CenterLng, c.Lat, c.Lng)
		if dist > region.RadiusMeters {
			t.Fatalf("收集品 %d 在区域 %s 外 (距离 %.1fm > 半径 %.0fm)", c.ID, c.RegionID, dist, region.RadiusMeters)
		}
	}
}

func TestCollectibleLookup(t *testing.T) {
	field, _ := newTestField(42)
	field.Populate()

	c, ok := field.Collectible(1)
	if !ok {
		t.Fatal("找不到 ID=1 的收集品")
	}
	if c.ID != 1 {
		t.Fatalf("Collectible.ID = %d, want 1", c.ID)
	}

	_, ok = field.Collectible(999)
	if ok {
		t.Fatal("不应该找到 ID=999 的收集品")
	}
}

func TestCollectiblesWithinRadius(t *testing.T) {
	field, _ := newTestField(42)
	field.Populate()

	// 查询 spawn 点 500m 半径
	results := field.CollectiblesWithinRadius(31.2304, 121.4737, 500)
	if len(results) == 0 {
		t.Fatal("期望至少一个收集品在 500m 内")
	}

	// 10m 半径应该很有限
	results10 := field.CollectiblesWithinRadius(31.2304, 121.4737, 10)
	if len(results10) > len(results) {
		t.Fatal("10m 查询结果不应多于 500m")
	}
}

func TestRemoveAndScheduleReplacement(t *testing.T) {
	field, nowPtr := newTestFieldWithTime(42, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	field.Populate()

	initialCount := field.Count()
	c, ok := field.Collectible(1)
	if !ok {
		t.Fatal("找不到 ID=1 的收集品")
	}

	// 移除前 1 号存在
	if !field.Remove(1) {
		t.Fatal("Remove(1) 失败")
	}
	if field.Count() != initialCount-1 {
		t.Fatalf("移除后 Count() = %d, want %d", field.Count(), initialCount-1)
	}
	_, ok = field.Collectible(1)
	if ok {
		t.Fatal("移除后 1 号应不存在")
	}

	// 立即推进，不应有重生（不到时间）
	spawned := field.AdvanceReplacements()
	if len(spawned) > 0 {
		t.Fatalf("未到期重生 = %d, want 0", len(spawned))
	}

	// 跳转到 6 秒后（min=5s, max=10s，确定性随机可能落在 5-10s 间）
	// 跳转 11 秒以确保到期
	*nowPtr = nowPtr.Add(11 * time.Second)
	spawned = field.AdvanceReplacements()
	if len(spawned) != 1 {
		t.Fatalf("到期后重生数 = %d, want 1", len(spawned))
	}
	if spawned[0].RegionID != c.RegionID {
		t.Fatalf("重生区域 = %s, want %s", spawned[0].RegionID, c.RegionID)
	}
	if field.Count() != initialCount {
		t.Fatalf("重生后 Count() = %d, want %d", field.Count(), initialCount)
	}
}

func TestRemoveNonexistent(t *testing.T) {
	field, _ := newTestField(42)
	field.Populate()

	if field.Remove(999) {
		t.Fatal("Remove(999) 应返回 false")
	}
}

func TestAdvanceReplacementsEmpty(t *testing.T) {
	field, _ := newTestField(42)
	spawned := field.AdvanceReplacements()
	if len(spawned) != 0 {
		t.Fatalf("空替换列表推进 = %d, want 0", len(spawned))
	}
}

func TestMultipleReplacements(t *testing.T) {
	field, nowPtr := newTestFieldWithTime(42, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	field.Populate()

	// 移除 5 个
	for id := uint64(1); id <= 5; id++ {
		if !field.Remove(id) {
			t.Fatalf("Remove(%d) 失败", id)
		}
	}
	if field.Count() != 10 {
		t.Fatalf("移除 5 个后 Count() = %d, want 10", field.Count())
	}

	// 推进 16 秒后全部重生
	*nowPtr = nowPtr.Add(16 * time.Second)
	spawned := field.AdvanceReplacements()
	if len(spawned) != 5 {
		t.Fatalf("重生数 = %d, want 5", len(spawned))
	}
	if field.Count() != 15 {
		t.Fatalf("全部重生后 Count() = %d, want 15", field.Count())
	}
}

func TestReplacementsCoalesce(t *testing.T) {
	// 验证多次移除和推进：所有到期项一起重生
	field, nowPtr := newTestFieldWithTime(42, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	field.Populate()

	field.Remove(1)
	field.Remove(2)
	field.Remove(3)

	if field.Count() != 12 {
		t.Fatalf("移除后 Count() = %d, want 12", field.Count())
	}

	*nowPtr = nowPtr.Add(20 * time.Second)
	spawned := field.AdvanceReplacements()
	if len(spawned) != 3 {
		t.Fatalf("合并重生数 = %d, want 3", len(spawned))
	}
}

func TestRegionsQuery(t *testing.T) {
	field, _ := newTestField(42)

	regions := field.Regions()
	if len(regions) != 3 {
		t.Fatalf("Regions() len = %d, want 3", len(regions))
	}

	r, ok := field.RegionByID("region-1")
	if !ok {
		t.Fatal("找不到 region-1")
	}
	if r.CenterLat != 31.2304 {
		t.Fatalf("region-1 CenterLat = %v, want 31.2304", r.CenterLat)
	}

	_, ok = field.RegionByID("nonexistent")
	if ok {
		t.Fatal("不应找到不存在的区域")
	}
}

func TestPlacementInsideCircle(t *testing.T) {
	// 重复验证 Populate 所有点位都在区域内
	for seed := uint64(0); seed < 20; seed++ {
		field, _ := newTestField(seed)
		created := field.Populate()
		for _, c := range created {
			region, _ := field.RegionByID(c.RegionID)
			dist := haversineMeters(region.CenterLat, region.CenterLng, c.Lat, c.Lng)
			if dist > region.RadiusMeters {
				t.Fatalf("seed=%d 收集品 %d 在区域外 (%.1fm > %.0fm)", seed, c.ID, dist, region.RadiusMeters)
			}
		}
	}
}

func TestReplacementPlacementInsideCircle(t *testing.T) {
	// 验证重生位置也在区域内
	field, nowPtr := newTestFieldWithTime(12345, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	field.Populate()

	// 移除全部
	for id := uint64(1); id <= 15; id++ {
		field.Remove(id)
	}

	*nowPtr = nowPtr.Add(20 * time.Second)
	spawned := field.AdvanceReplacements()
	if len(spawned) != 15 {
		t.Fatalf("重生数 = %d, want 15", len(spawned))
	}

	for _, c := range spawned {
		region, _ := field.RegionByID(c.RegionID)
		dist := haversineMeters(region.CenterLat, region.CenterLng, c.Lat, c.Lng)
		if dist > region.RadiusMeters {
			t.Fatalf("重生收集品 %d 在区域外 (%.1fm > %.0fm)", c.ID, dist, region.RadiusMeters)
		}
	}
}

func TestGridQueryEfficiency(t *testing.T) {
	// 验证 500m 查询只检查九格候选而非全部收集品
	field, _ := newTestField(42)
	field.Populate()

	// 在 spawn 点 500m 半径内应该有 region-1 的收集品
	results := field.CollectiblesWithinRadius(31.2304, 121.4737, 500)
	for _, c := range results {
		dist := haversineMeters(31.2304, 121.4737, c.Lat, c.Lng)
		if dist > 500 {
			t.Fatalf("查询返回的收集品 %d 距离 %.1fm > 500m", c.ID, dist)
		}
	}

	// 600m 查询应包含更多结果
	results600 := field.CollectiblesWithinRadius(31.2304, 121.4737, 600)
	if len(results600) < len(results) {
		t.Fatalf("600m 查询结果(%d)应 >= 500m 查询结果(%d)", len(results600), len(results))
	}
}

func TestTenMeterPickupQuery(t *testing.T) {
	field, _ := newTestField(42)
	field.Populate()

	// 在 spawn 点 10m 半径内可能没有物品（随机放置）
	results := field.CollectiblesWithinRadius(31.2304, 121.4737, 10)
	for _, c := range results {
		dist := haversineMeters(31.2304, 121.4737, c.Lat, c.Lng)
		if dist > 10 {
			t.Fatalf("10m 查询返回的收集品 %d 距离 %.1fm > 10m", c.ID, dist)
		}
	}
}

func TestNextIDIncrement(t *testing.T) {
	field, _ := newTestField(42)
	created := field.Populate()

	ids := make([]uint64, len(created))
	for i, c := range created {
		ids[i] = c.ID
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for i, id := range ids {
		if id != uint64(i+1) {
			t.Fatalf("ID 序列不连续: ids[%d] = %d, want %d", i, id, i+1)
		}
	}
}
