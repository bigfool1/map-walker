package game

import (
	"math"
	"sort"
	"time"
)

const metersPerDegreeLatitude = 111_320.0

type Config struct {
	SpawnLat             float64
	SpawnLng             float64
	SpeedMetersPerSecond float64
}

func DefaultConfig() Config {
	return Config{
		SpawnLat:             31.2304,
		SpawnLng:             121.4737,
		SpeedMetersPerSecond: 60,
	}
}

type InputState struct {
	Sequence uint64
	Up       bool
	Down     bool
	Left     bool
	Right    bool
}

type PlayerPosition struct {
	ID  string  `json:"id"`
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type Snapshot struct {
	Tick    uint64
	Players []PlayerPosition
}

type Delta struct {
	Tick             uint64
	Players          []PlayerPosition
	RemovedPlayerIDs []string
}

func (d Delta) HasChanges() bool {
	return len(d.Players) > 0 || len(d.RemovedPlayerIDs) > 0
}

type player struct {
	position     PlayerPosition
	input        InputState
	lastSequence uint64
}

type World struct {
	config           Config
	players          map[string]*player
	tick             uint64
	dirtyPlayerIDs   map[string]struct{}
	removedPlayerIDs map[string]struct{}
}

func NewWorld(config Config) *World {
	return &World{
		config:           config,
		players:          map[string]*player{},
		dirtyPlayerIDs:   map[string]struct{}{},
		removedPlayerIDs: map[string]struct{}{},
	}
}

func (w *World) AddPlayer(playerID string) bool {
	if _, exists := w.players[playerID]; exists {
		return false
	}

	w.players[playerID] = &player{
		position: PlayerPosition{
			ID:  playerID,
			Lat: w.config.SpawnLat,
			Lng: w.config.SpawnLng,
		},
	}
	w.dirtyPlayerIDs[playerID] = struct{}{}
	delete(w.removedPlayerIDs, playerID)
	return true
}

func (w *World) RemovePlayer(playerID string) bool {
	if _, exists := w.players[playerID]; !exists {
		return false
	}

	delete(w.players, playerID)
	delete(w.dirtyPlayerIDs, playerID)
	w.removedPlayerIDs[playerID] = struct{}{}
	return true
}

func (w *World) ResetInput(playerID string) {
	p, exists := w.players[playerID]
	if !exists {
		return
	}
	p.input = InputState{}
	p.lastSequence = 0
}

func (w *World) ApplyInput(playerID string, input InputState) bool {
	p, exists := w.players[playerID]
	if !exists || input.Sequence <= p.lastSequence {
		return false
	}

	p.input = input
	p.lastSequence = input.Sequence
	return true
}

func (w *World) Step(deltaTime time.Duration) {
	w.tick += 1
	distance := w.config.SpeedMetersPerSecond * deltaTime.Seconds()

	for playerID, p := range w.players {
		x := boolNumber(p.input.Right) - boolNumber(p.input.Left)
		y := boolNumber(p.input.Up) - boolNumber(p.input.Down)
		if x == 0 && y == 0 {
			continue
		}

		length := math.Hypot(x, y)
		x /= length
		y /= length

		p.position.Lat += y * distance / metersPerDegreeLatitude
		p.position.Lng += x * distance / metersPerDegreeLongitude(p.position.Lat)
		w.dirtyPlayerIDs[playerID] = struct{}{}
	}
}

func (w *World) Snapshot() Snapshot {
	return Snapshot{
		Tick:    w.tick,
		Players: w.positionsFor(w.playersKeys()),
	}
}

func (w *World) TakeDelta() Delta {
	playerIDs := setKeys(w.dirtyPlayerIDs)
	removedIDs := setKeys(w.removedPlayerIDs)

	delta := Delta{
		Tick:             w.tick,
		Players:          w.positionsFor(playerIDs),
		RemovedPlayerIDs: removedIDs,
	}

	clear(w.dirtyPlayerIDs)
	clear(w.removedPlayerIDs)
	return delta
}

func (w *World) PlayerCount() int {
	return len(w.players)
}

func (w *World) playersKeys() []string {
	ids := make([]string, 0, len(w.players))
	for id := range w.players {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (w *World) positionsFor(ids []string) []PlayerPosition {
	positions := make([]PlayerPosition, 0, len(ids))
	for _, id := range ids {
		if p, exists := w.players[id]; exists {
			positions = append(positions, p.position)
		}
	}
	return positions
}

func setKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func boolNumber(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func metersPerDegreeLongitude(latitude float64) float64 {
	return metersPerDegreeLatitude * math.Cos(latitude*math.Pi/180)
}
