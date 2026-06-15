package game

import (
	"math"
	"math/rand/v2"
	"time"
)

// Collectible 是一个可收集物品的运行时实例
type Collectible struct {
	ID       uint64
	RegionID string
	Lat      float64
	Lng      float64
}

const minCollectibleSeparationMeters = 0.5

type replacementDeadline struct {
	regionID string
	dueAt    time.Time
}

// CollectibleField 是纯逻辑的收集品字段，无数据库/连接/goroutine/锁依赖
type CollectibleField struct {
	aoiConfig    AOIConfig
	regions      []CollectibleRegion
	collectibles map[uint64]*Collectible
	grid         *collectibleGrid
	replacements []replacementDeadline
	nextID       uint64
	timeNow      func() time.Time
	rng          *rand.Rand
}

// NewCollectibleField 创建收集品字段
// timeNow 和 rng 是测试接缝，传 nil 使用真实实现
func NewCollectibleField(aoiConfig AOIConfig, regions []CollectibleRegion, timeNow func() time.Time, rng *rand.Rand) *CollectibleField {
	if timeNow == nil {
		timeNow = time.Now
	}
	if rng == nil {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	return &CollectibleField{
		aoiConfig:    aoiConfig,
		regions:      regions,
		collectibles: make(map[uint64]*Collectible),
		grid:         newCollectibleGrid(aoiConfig),
		nextID:       1,
		timeNow:      timeNow,
		rng:          rng,
	}
}

// Populate 为每个区域填充至目标数量，返回创建的所有收集品
func (f *CollectibleField) Populate() []Collectible {
	var all []Collectible
	for i := range f.regions {
		region := &f.regions[i]
		for range region.TargetCount {
			c := f.spawnInRegion(region)
			all = append(all, c)
		}
	}
	return all
}

// Collectible 按 ID 查找收集品
func (f *CollectibleField) Collectible(id uint64) (Collectible, bool) {
	c, ok := f.collectibles[id]
	if !ok {
		return Collectible{}, false
	}
	return *c, true
}

// CollectiblesWithinRadius 返回指定点半径内的所有收集品
func (f *CollectibleField) CollectiblesWithinRadius(lat, lng, radiusMeters float64) []Collectible {
	candidates := f.grid.candidateIDs(lat, lng)
	radiusSq := radiusMeters * radiusMeters
	result := make([]Collectible, 0, len(candidates))
	for _, id := range candidates {
		c := f.collectibles[id]
		if f.distanceSq(lat, lng, c.Lat, c.Lng) <= radiusSq {
			result = append(result, *c)
		}
	}
	return result
}

// Regions 返回所有区域配置的副本
func (f *CollectibleField) Regions() []CollectibleRegion {
	out := make([]CollectibleRegion, len(f.regions))
	copy(out, f.regions)
	return out
}

// RegionByID 按 ID 查找区域配置
func (f *CollectibleField) RegionByID(id string) (CollectibleRegion, bool) {
	for _, r := range f.regions {
		if r.ID == id {
			return r, true
		}
	}
	return CollectibleRegion{}, false
}

// Count 返回当前存活收集品数量
func (f *CollectibleField) Count() int {
	return len(f.collectibles)
}

// Remove 移除收集品（拾取），调度替换重生，返回是否成功
func (f *CollectibleField) Remove(id uint64) bool {
	c, ok := f.collectibles[id]
	if !ok {
		return false
	}

	f.grid.remove(c.ID, c.Lat, c.Lng)
	delete(f.collectibles, c.ID)

	region, ok := f.RegionByID(c.RegionID)
	if !ok {
		return true
	}

	delay := region.RespawnMin + time.Duration(f.rng.IntN(int(region.RespawnMax-region.RespawnMin)+1))
	f.replacements = append(f.replacements, replacementDeadline{
		regionID: c.RegionID,
		dueAt:    f.timeNow().Add(delay),
	})
	return true
}

// AdvanceReplacements 推进到期替换，返回新生成的收集品
func (f *CollectibleField) AdvanceReplacements() []Collectible {
	now := f.timeNow()
	var due []int
	for i, r := range f.replacements {
		if !r.dueAt.After(now) {
			due = append(due, i)
		}
	}
	if len(due) == 0 {
		return nil
	}

	spawned := make([]Collectible, 0, len(due))
	// 从后往前删除，避免索引失效
	for idx := len(due) - 1; idx >= 0; idx-- {
		i := due[idx]
		region, ok := f.RegionByID(f.replacements[i].regionID)
		f.replacements = append(f.replacements[:i], f.replacements[i+1:]...)
		if !ok {
			continue
		}
		c := f.spawnInRegion(&region)
		spawned = append(spawned, c)
	}
	return spawned
}

func (f *CollectibleField) spawnInRegion(region *CollectibleRegion) Collectible {
	lat, lng := f.placeInRegion(region)
	id := f.nextID
	f.nextID++

	c := &Collectible{ID: id, RegionID: region.ID, Lat: lat, Lng: lng}
	f.collectibles[id] = c
	f.grid.insert(id, lat, lng)
	return *c
}

// placeInRegion 在区域内采样位置，最多尝试 10 次避免与其他收集品过近
func (f *CollectibleField) placeInRegion(region *CollectibleRegion) (lat, lng float64) {
	const maxAttempts = 10
	for attempt := 0; attempt < maxAttempts; attempt++ {
		lat, lng = f.randomPointInCircle(region.CenterLat, region.CenterLng, region.RadiusMeters)
		if !f.hasNearbyCollectible(lat, lng, minCollectibleSeparationMeters) {
			return lat, lng
		}
	}
	return f.randomPointInCircle(region.CenterLat, region.CenterLng, region.RadiusMeters)
}

// randomPointInCircle 在圆形区域内均匀采样
func (f *CollectibleField) randomPointInCircle(centerLat, centerLng, radiusMeters float64) (lat, lng float64) {
	u := f.rng.Float64()
	v := f.rng.Float64()
	r := radiusMeters * math.Sqrt(u)
	theta := 2 * math.Pi * v

	localX := r * math.Cos(theta)
	localY := r * math.Sin(theta)

	cx, cy := f.aoiConfig.LatLngToLocal(centerLat, centerLng)
	return f.aoiConfig.LocalToLatLng(cx+localX, cy+localY)
}

func (f *CollectibleField) hasNearbyCollectible(lat, lng, radiusMeters float64) bool {
	candidates := f.grid.candidateIDs(lat, lng)
	radiusSq := radiusMeters * radiusMeters
	for _, id := range candidates {
		c := f.collectibles[id]
		if f.distanceSq(lat, lng, c.Lat, c.Lng) <= radiusSq {
			return true
		}
	}
	return false
}

func (f *CollectibleField) distanceSq(lat1, lng1, lat2, lng2 float64) float64 {
	x1, y1 := f.aoiConfig.LatLngToLocal(lat1, lng1)
	x2, y2 := f.aoiConfig.LatLngToLocal(lat2, lng2)
	dx := x1 - x2
	dy := y1 - y2
	return dx*dx + dy*dy
}

// collectibleGrid 收集品空间索引（600m 网格）
type collectibleGrid struct {
	aoiConfig AOIConfig
	cells     map[CellCoord]map[uint64]struct{}
}

func newCollectibleGrid(aoiConfig AOIConfig) *collectibleGrid {
	return &collectibleGrid{
		aoiConfig: aoiConfig,
		cells:     make(map[CellCoord]map[uint64]struct{}),
	}
}

func (g *collectibleGrid) insert(id uint64, lat, lng float64) {
	localX, localY := g.aoiConfig.LatLngToLocal(lat, lng)
	cell := g.aoiConfig.localToCell(localX, localY)
	g.addToCell(id, cell)
}

func (g *collectibleGrid) remove(id uint64, lat, lng float64) {
	localX, localY := g.aoiConfig.LatLngToLocal(lat, lng)
	cell := g.aoiConfig.localToCell(localX, localY)
	g.removeFromCell(id, cell)
}

func (g *collectibleGrid) candidateIDs(lat, lng float64) []uint64 {
	localX, localY := g.aoiConfig.LatLngToLocal(lat, lng)
	centerCell := g.aoiConfig.localToCell(localX, localY)

	var ids []uint64
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			cell := CellCoord{X: centerCell.X + dx, Y: centerCell.Y + dy}
			for id := range g.cells[cell] {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (g *collectibleGrid) addToCell(id uint64, cell CellCoord) {
	if g.cells[cell] == nil {
		g.cells[cell] = make(map[uint64]struct{})
	}
	g.cells[cell][id] = struct{}{}
}

func (g *collectibleGrid) removeFromCell(id uint64, cell CellCoord) {
	members, ok := g.cells[cell]
	if !ok {
		return
	}
	delete(members, id)
	if len(members) == 0 {
		delete(g.cells, cell)
	}
}
