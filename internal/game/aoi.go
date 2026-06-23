package game

import (
	"math"
)

const (
	defaultCellSizeMeters             = 600
	defaultEnterRadiusMeters          = 500
	defaultLeaveRadiusMeters          = 600
	defaultEnterRescanDistanceMeters  = 50
)

type CellCoord struct {
	X int
	Y int
}

type AOIConfig struct {
	OriginLat                 float64
	OriginLng                 float64
	CellSizeMeters            float64
	EnterRadiusMeters         float64
	LeaveRadiusMeters         float64
	EnterRescanDistanceMeters float64
}

func AOIConfigFromWorld(config Config) AOIConfig {
	return AOIConfig{
		OriginLat:                 config.SpawnLat,
		OriginLng:                 config.SpawnLng,
		CellSizeMeters:            defaultCellSizeMeters,
		EnterRadiusMeters:         defaultEnterRadiusMeters,
		LeaveRadiusMeters:         defaultLeaveRadiusMeters,
		EnterRescanDistanceMeters: defaultEnterRescanDistanceMeters,
	}
}

type RelationshipChanges struct {
	Entered []int64
	Left    []int64
}

// MovementDelta 描述一次移动的 AOI 关系变化。
// Entered 和 Left 与 RelationshipChanges 含义一致。
// Stable 包含移动前后都可见的邻居，用于位置扇出。
type MovementDelta struct {
	PlayerID int64
	Entered  []int64
	Left     []int64
	Stable   []int64
}

type AOIStats struct {
	CandidatePairs       uint64
	DistanceChecks       uint64
	RelationshipsEntered uint64
	RelationshipsLeft    uint64
	FullEnterScans       uint64
	SkippedEnterScans    uint64
	LeaveChecks          uint64
	StableRelationships  uint64
}

type aoiPlayer struct {
	lat, lng            float64
	localX, localY      float64
	cell                CellCoord
	lastEnterScanX      float64
	lastEnterScanY      float64
	lastEnterScanCell   CellCoord
	hasEnterScanMarker  bool
}

type AOIIndex struct {
	config  AOIConfig
	players map[int64]*aoiPlayer
	cells   map[CellCoord]map[int64]struct{}
	visible map[int64]map[int64]struct{}
	stats   AOIStats
}

func NewAOIIndex(config AOIConfig) *AOIIndex {
	return &AOIIndex{
		config:  config,
		players: map[int64]*aoiPlayer{},
		cells:   map[CellCoord]map[int64]struct{}{},
		visible: map[int64]map[int64]struct{}{},
	}
}

func (a *AOIIndex) HasPlayer(playerID int64) bool {
	_, exists := a.players[playerID]
	return exists
}

func (a *AOIIndex) Cell(playerID int64) (CellCoord, bool) {
	p, exists := a.players[playerID]
	if !exists {
		return CellCoord{}, false
	}
	return p.cell, true
}

func (a *AOIIndex) LocalPosition(playerID int64) (localX, localY float64, ok bool) {
	p, exists := a.players[playerID]
	if !exists {
		return 0, 0, false
	}
	return p.localX, p.localY, true
}

func (a *AOIIndex) VisibleNeighbors(playerID int64) []int64 {
	neighbors := a.visible[playerID]
	if len(neighbors) == 0 {
		return nil
	}
	out := make([]int64, 0, len(neighbors))
	for neighborID := range neighbors {
		out = append(out, neighborID)
	}
	return out
}

// QueryPlayerIDsNearPoint 返回指定点周围九格内的所有玩家 ID
// 用于收集品反向扇出（spawn/collect 通知附近玩家）
func (a *AOIIndex) QueryPlayerIDsNearPoint(lat, lng float64) []int64 {
	localX, localY := a.config.LatLngToLocal(lat, lng)
	centerCell := a.config.localToCell(localX, localY)

	var ids []int64
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			cell := CellCoord{X: centerCell.X + dx, Y: centerCell.Y + dy}
			for id := range a.cells[cell] {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (a *AOIIndex) TakeStats() AOIStats {
	stats := a.stats
	a.stats = AOIStats{}
	return stats
}

func (a *AOIIndex) VisibleRelationshipPairs() uint64 {
	var total uint64
	for _, neighbors := range a.visible {
		total += uint64(len(neighbors))
	}
	return total / 2
}

func (a *AOIIndex) Insert(playerID int64, lat, lng float64) RelationshipChanges {
	if _, exists := a.players[playerID]; exists {
		return RelationshipChanges{}
	}
	a.setPosition(playerID, lat, lng)
	return a.recalculateRelationships(playerID)
}

func (a *AOIIndex) Move(playerID int64, lat, lng float64) RelationshipChanges {
	delta := a.MoveDetailed(playerID, lat, lng)
	return RelationshipChanges{Entered: delta.Entered, Left: delta.Left}
}

// MoveDetailed 返回移动的完整关系变化，包含 stable 邻居。
// 小幅同 cell 移动会跳过九格 enter 扫描以降低开销；leave 检测始终精确执行。
func (a *AOIIndex) MoveDetailed(playerID int64, lat, lng float64) MovementDelta {
	self, exists := a.players[playerID]
	if !exists {
		return MovementDelta{PlayerID: playerID}
	}

	oldVisible := a.visible[playerID]
	oldNeighborIDs := make([]int64, 0, len(oldVisible))
	for nid := range oldVisible {
		oldNeighborIDs = append(oldNeighborIDs, nid)
	}

	a.setPosition(playerID, lat, lng)
	self = a.players[playerID]

	left := a.leaveCheckExistingNeighbors(self, playerID)

	var entered []int64
	if a.shouldForceEnterScan(self) {
		entered = a.enterScanNineCells(self, playerID)
		a.stats.FullEnterScans++
		a.markEnterScan(self)
	} else {
		a.stats.SkippedEnterScans++
	}

	leftSet := make(map[int64]struct{}, len(left))
	for _, id := range left {
		leftSet[id] = struct{}{}
	}
	stable := make([]int64, 0, len(oldNeighborIDs))
	for _, id := range oldNeighborIDs {
		if _, isLeft := leftSet[id]; isLeft {
			continue
		}
		stable = append(stable, id)
	}
	a.stats.StableRelationships += uint64(len(stable))

	return MovementDelta{
		PlayerID: playerID,
		Entered:  entered,
		Left:     left,
		Stable:   stable,
	}
}

func (a *AOIIndex) RecalculateRelationships(playerID int64) RelationshipChanges {
	if _, exists := a.players[playerID]; !exists {
		return RelationshipChanges{}
	}
	return a.recalculateRelationships(playerID)
}

func (a *AOIIndex) Remove(playerID int64) RelationshipChanges {
	p, exists := a.players[playerID]
	if !exists {
		return RelationshipChanges{}
	}

	left := setKeys(a.visible[playerID])
	for _, neighborID := range left {
		a.removeRelationship(neighborID, playerID)
	}

	a.removeFromCell(playerID, p.cell)
	delete(a.players, playerID)
	delete(a.visible, playerID)

	return RelationshipChanges{Left: left}
}

func (a *AOIIndex) setPosition(playerID int64, lat, lng float64) {
	localX, localY := a.config.LatLngToLocal(lat, lng)
	cell := a.config.localToCell(localX, localY)

	if existing, ok := a.players[playerID]; ok {
		if existing.cell != cell {
			a.removeFromCell(playerID, existing.cell)
			a.addToCell(playerID, cell)
		}
		existing.lat = lat
		existing.lng = lng
		existing.localX = localX
		existing.localY = localY
		existing.cell = cell
		return
	}

	a.players[playerID] = &aoiPlayer{
		lat:    lat,
		lng:    lng,
		localX: localX,
		localY: localY,
		cell:   cell,
	}
	a.addToCell(playerID, cell)
}

// enterScanNineCells 对 self 所在九格中尚未可见的候选者执行进入半径检查。
// 返回新进入的邻居 ID。调用方负责递增 FullEnterScans。
func (a *AOIIndex) enterScanNineCells(self *aoiPlayer, playerID int64) []int64 {
	entered := make([]int64, 0)
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for candidateID := range a.cells[CellCoord{X: self.cell.X + dx, Y: self.cell.Y + dy}] {
				if candidateID == playerID || a.IsVisible(playerID, candidateID) {
					continue
				}
				a.stats.CandidatePairs++
				candidate := a.players[candidateID]
				a.stats.DistanceChecks++
				if a.withinEnterRadius(self, candidate) {
					if a.addRelationship(playerID, candidateID) {
						entered = append(entered, candidateID)
						a.stats.RelationshipsEntered++
					}
				}
			}
		}
	}
	return entered
}

// leaveCheckExistingNeighbors 检查当前可见邻居是否超出离开半径。
// 返回离开的邻居 ID。
func (a *AOIIndex) leaveCheckExistingNeighbors(self *aoiPlayer, playerID int64) []int64 {
	left := make([]int64, 0)
	for neighborID := range a.visible[playerID] {
		neighbor := a.players[neighborID]
		if neighbor == nil {
			continue
		}
		a.stats.DistanceChecks++
		a.stats.LeaveChecks++
		if a.beyondLeaveRadius(self, neighbor) {
			left = append(left, neighborID)
		}
	}
	for _, neighborID := range left {
		if a.removeRelationship(playerID, neighborID) {
			a.stats.RelationshipsLeft++
		}
	}
	return left
}

// shouldForceEnterScan 判断是否需要强制 enter 扫描。
// 条件：无扫描标记、换 cell、或距上次扫描超过阈值。
func (a *AOIIndex) shouldForceEnterScan(self *aoiPlayer) bool {
	if !self.hasEnterScanMarker {
		return true
	}
	if self.cell != self.lastEnterScanCell {
		return true
	}
	dx := self.localX - self.lastEnterScanX
	dy := self.localY - self.lastEnterScanY
	return dx*dx+dy*dy >= a.config.enterRescanDistanceSquared()
}

func (a *AOIIndex) markEnterScan(self *aoiPlayer) {
	self.lastEnterScanX = self.localX
	self.lastEnterScanY = self.localY
	self.lastEnterScanCell = self.cell
	self.hasEnterScanMarker = true
}

func (a *AOIIndex) recalculateRelationships(playerID int64) RelationshipChanges {
	self, exists := a.players[playerID]
	if !exists {
		return RelationshipChanges{}
	}
	entered := a.enterScanNineCells(self, playerID)
	a.stats.FullEnterScans++
	a.markEnterScan(self)
	left := a.leaveCheckExistingNeighbors(self, playerID)
	return RelationshipChanges{Entered: entered, Left: left}
}

func (a *AOIIndex) IsVisible(playerA, playerB int64) bool {
	_, exists := a.visible[playerA][playerB]
	return exists
}

func (a *AOIIndex) addRelationship(playerA, playerB int64) bool {
	if a.IsVisible(playerA, playerB) {
		return false
	}
	a.ensureVisibleSet(playerA)[playerB] = struct{}{}
	a.ensureVisibleSet(playerB)[playerA] = struct{}{}
	return true
}

func (a *AOIIndex) removeRelationship(playerA, playerB int64) bool {
	if !a.IsVisible(playerA, playerB) {
		return false
	}
	delete(a.visible[playerA], playerB)
	if len(a.visible[playerA]) == 0 {
		delete(a.visible, playerA)
	}
	delete(a.visible[playerB], playerA)
	if len(a.visible[playerB]) == 0 {
		delete(a.visible, playerB)
	}
	return true
}

func (a *AOIIndex) ensureVisibleSet(playerID int64) map[int64]struct{} {
	if a.visible[playerID] == nil {
		a.visible[playerID] = map[int64]struct{}{}
	}
	return a.visible[playerID]
}

func (a *AOIIndex) addToCell(playerID int64, cell CellCoord) {
	if a.cells[cell] == nil {
		a.cells[cell] = map[int64]struct{}{}
	}
	a.cells[cell][playerID] = struct{}{}
}

func (a *AOIIndex) removeFromCell(playerID int64, cell CellCoord) {
	members, ok := a.cells[cell]
	if !ok {
		return
	}
	delete(members, playerID)
	if len(members) == 0 {
		delete(a.cells, cell)
	}
}

func (a *AOIIndex) withinEnterRadius(aPlayer, bPlayer *aoiPlayer) bool {
	return a.distanceSquared(aPlayer, bPlayer) <= a.config.enterRadiusSquared()
}

func (a *AOIIndex) beyondLeaveRadius(aPlayer, bPlayer *aoiPlayer) bool {
	return a.distanceSquared(aPlayer, bPlayer) > a.config.leaveRadiusSquared()
}

func (a *AOIIndex) distanceSquared(aPlayer, bPlayer *aoiPlayer) float64 {
	dx := aPlayer.localX - bPlayer.localX
	dy := aPlayer.localY - bPlayer.localY
	return dx*dx + dy*dy
}

func (c AOIConfig) LatLngToLocal(lat, lng float64) (localX, localY float64) {
	localY = (lat - c.OriginLat) * metersPerDegreeLatitude
	localX = (lng - c.OriginLng) * metersPerDegreeLongitude(c.OriginLat)
	return localX, localY
}

func (c AOIConfig) localToCell(localX, localY float64) CellCoord {
	return CellCoord{
		X: int(math.Floor(localX / c.CellSizeMeters)),
		Y: int(math.Floor(localY / c.CellSizeMeters)),
	}
}

func (c AOIConfig) LocalToLatLng(localX, localY float64) (lat, lng float64) {
	lat = c.OriginLat + localY/metersPerDegreeLatitude
	lng = c.OriginLng + localX/metersPerDegreeLongitude(c.OriginLat)
	return lat, lng
}

func (c AOIConfig) enterRadiusSquared() float64 {
	return c.EnterRadiusMeters * c.EnterRadiusMeters
}

func (c AOIConfig) leaveRadiusSquared() float64 {
	return c.LeaveRadiusMeters * c.LeaveRadiusMeters
}

func (c AOIConfig) enterRescanDistanceSquared() float64 {
	return c.EnterRescanDistanceMeters * c.EnterRescanDistanceMeters
}
