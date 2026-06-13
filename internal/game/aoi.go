package game

import (
	"math"
	"sort"
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
	Entered []string
	Left    []string
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
	players map[string]*aoiPlayer
	cells   map[CellCoord]map[string]struct{}
	visible map[string]map[string]struct{}
	stats   AOIStats
}

func NewAOIIndex(config AOIConfig) *AOIIndex {
	return &AOIIndex{
		config:  config,
		players: map[string]*aoiPlayer{},
		cells:   map[CellCoord]map[string]struct{}{},
		visible: map[string]map[string]struct{}{},
	}
}

func (a *AOIIndex) HasPlayer(playerID string) bool {
	_, exists := a.players[playerID]
	return exists
}

func (a *AOIIndex) Cell(playerID string) (CellCoord, bool) {
	p, exists := a.players[playerID]
	if !exists {
		return CellCoord{}, false
	}
	return p.cell, true
}

func (a *AOIIndex) LocalPosition(playerID string) (localX, localY float64, ok bool) {
	p, exists := a.players[playerID]
	if !exists {
		return 0, 0, false
	}
	return p.localX, p.localY, true
}

func (a *AOIIndex) VisibleNeighbors(playerID string) []string {
	neighbors := setKeys(a.visible[playerID])
	return neighbors
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

func (a *AOIIndex) Insert(playerID string, lat, lng float64) RelationshipChanges {
	if _, exists := a.players[playerID]; exists {
		return RelationshipChanges{}
	}
	a.setPosition(playerID, lat, lng)
	return a.recalculateRelationships(playerID)
}

func (a *AOIIndex) Move(playerID string, lat, lng float64) RelationshipChanges {
	if _, exists := a.players[playerID]; !exists {
		return RelationshipChanges{}
	}
	a.setPosition(playerID, lat, lng)
	return a.recalculateRelationships(playerID)
}

func (a *AOIIndex) RecalculateRelationships(playerID string) RelationshipChanges {
	if _, exists := a.players[playerID]; !exists {
		return RelationshipChanges{}
	}
	return a.recalculateRelationships(playerID)
}

func (a *AOIIndex) Remove(playerID string) RelationshipChanges {
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

func (a *AOIIndex) setPosition(playerID string, lat, lng float64) {
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

func (a *AOIIndex) recalculateRelationships(playerID string) RelationshipChanges {
	self, exists := a.players[playerID]
	if !exists {
		return RelationshipChanges{}
	}

	entered := make([]string, 0)
	left := make([]string, 0)

	for _, candidateID := range a.nineCellCandidates(self.cell) {
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

	for _, neighborID := range setKeys(a.visible[playerID]) {
		neighbor := a.players[neighborID]
		if neighbor == nil {
			continue
		}
		a.stats.DistanceChecks++
		if a.beyondLeaveRadius(self, neighbor) {
			if a.removeRelationship(playerID, neighborID) {
				left = append(left, neighborID)
				a.stats.RelationshipsLeft++
			}
		}
	}

	return RelationshipChanges{
		Entered: sortedCopy(entered),
		Left:    sortedCopy(left),
	}
}

func (a *AOIIndex) nineCellCandidates(cell CellCoord) []string {
	seen := map[string]struct{}{}
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for id := range a.cells[CellCoord{X: cell.X + dx, Y: cell.Y + dy}] {
				seen[id] = struct{}{}
			}
		}
	}
	return setKeys(seen)
}

func (a *AOIIndex) IsVisible(playerA, playerB string) bool {
	_, exists := a.visible[playerA][playerB]
	return exists
}

func (a *AOIIndex) addRelationship(playerA, playerB string) bool {
	if a.IsVisible(playerA, playerB) {
		return false
	}
	a.ensureVisibleSet(playerA)[playerB] = struct{}{}
	a.ensureVisibleSet(playerB)[playerA] = struct{}{}
	return true
}

func (a *AOIIndex) removeRelationship(playerA, playerB string) bool {
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

func (a *AOIIndex) ensureVisibleSet(playerID string) map[string]struct{} {
	if a.visible[playerID] == nil {
		a.visible[playerID] = map[string]struct{}{}
	}
	return a.visible[playerID]
}

func (a *AOIIndex) addToCell(playerID string, cell CellCoord) {
	if a.cells[cell] == nil {
		a.cells[cell] = map[string]struct{}{}
	}
	a.cells[cell][playerID] = struct{}{}
}

func (a *AOIIndex) removeFromCell(playerID string, cell CellCoord) {
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

func sortedCopy(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
