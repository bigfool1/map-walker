package game

import (
	"math"
)

const (
	defaultCellSizeMeters    = 600
	defaultEnterRadiusMeters = 500
	defaultLeaveRadiusMeters = 600
)

type CellCoord struct {
	X int
	Y int
}

type AOIConfig struct {
	OriginLat         float64
	OriginLng         float64
	CellSizeMeters    float64
	EnterRadiusMeters float64
	LeaveRadiusMeters float64
}

func AOIConfigFromWorld(config Config) AOIConfig {
	return AOIConfig{
		OriginLat:         config.SpawnLat,
		OriginLng:         config.SpawnLng,
		CellSizeMeters:    defaultCellSizeMeters,
		EnterRadiusMeters: defaultEnterRadiusMeters,
		LeaveRadiusMeters: defaultLeaveRadiusMeters,
	}
}

type RelationshipChanges struct {
	Entered []int64
	Left    []int64
}

type AOIStats struct {
	CandidatePairs       uint64
	DistanceChecks       uint64
	RelationshipsEntered uint64
	RelationshipsLeft    uint64
}

type aoiPlayer struct {
	lat, lng       float64
	localX, localY float64
	cell           CellCoord
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
	localX, localY := a.config.latLngToLocal(lat, lng)
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
	if _, exists := a.players[playerID]; !exists {
		return RelationshipChanges{}
	}
	a.setPosition(playerID, lat, lng)
	return a.recalculateRelationships(playerID)
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
	localX, localY := a.config.latLngToLocal(lat, lng)
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

func (a *AOIIndex) recalculateRelationships(playerID int64) RelationshipChanges {
	self, exists := a.players[playerID]
	if !exists {
		return RelationshipChanges{}
	}

	entered := make([]int64, 0)
	left := make([]int64, 0)

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

	for neighborID := range a.visible[playerID] {
		neighbor := a.players[neighborID]
		if neighbor == nil {
			continue
		}
		a.stats.DistanceChecks++
		if a.beyondLeaveRadius(self, neighbor) {
			left = append(left, neighborID)
		}
	}
	for _, neighborID := range left {
		if a.removeRelationship(playerID, neighborID) {
			a.stats.RelationshipsLeft++
		}
	}

	return RelationshipChanges{
		Entered: entered,
		Left:    left,
	}
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

func (c AOIConfig) latLngToLocal(lat, lng float64) (localX, localY float64) {
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
