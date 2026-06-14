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
	ID  int64   `json:"id"`
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type PlayerState struct {
	ID         int64      `json:"id"`
	Username   string     `json:"username"`
	Lat        float64    `json:"lat"`
	Lng        float64    `json:"lng"`
	Appearance Appearance `json:"appearance"`
}

type player struct {
	position     PlayerPosition
	username     string
	appearance   Appearance
	input        InputState
	lastSequence uint64
}

type World struct {
	config           Config
	players          map[int64]*player
	tick             uint64
	movedPlayerIDs   map[int64]struct{}
	removedPlayerIDs map[int64]struct{}
}

func NewWorld(config Config) *World {
	return &World{
		config:           config,
		players:          map[int64]*player{},
		movedPlayerIDs:   map[int64]struct{}{},
		removedPlayerIDs: map[int64]struct{}{},
	}
}

func (w *World) Config() Config {
	return w.config
}

func (w *World) Tick() uint64 {
	return w.tick
}

func (w *World) SpawnLatLng() (float64, float64) {
	return w.config.SpawnLat, w.config.SpawnLng
}

func (w *World) AddPlayer(playerID int64) bool {
	return w.AddPlayerWithState(playerID, "", w.config.SpawnLat, w.config.SpawnLng, DefaultAppearance())
}

func (w *World) AddPlayerAt(playerID int64, lat, lng float64) bool {
	return w.AddPlayerWithState(playerID, "", lat, lng, DefaultAppearance())
}

func (w *World) AddPlayerWithAppearance(playerID int64, lat, lng float64, appearance Appearance) bool {
	return w.AddPlayerWithState(playerID, "", lat, lng, appearance)
}

func (w *World) AddPlayerWithState(playerID int64, username string, lat, lng float64, appearance Appearance) bool {
	if _, exists := w.players[playerID]; exists {
		return false
	}

	w.players[playerID] = &player{
		position: PlayerPosition{
			ID:  playerID,
			Lat: lat,
			Lng: lng,
		},
		username:   username,
		appearance: appearance,
	}
	delete(w.removedPlayerIDs, playerID)
	return true
}

func (w *World) HasPlayer(playerID int64) bool {
	_, exists := w.players[playerID]
	return exists
}

func (w *World) RemovePlayer(playerID int64) bool {
	if _, exists := w.players[playerID]; !exists {
		return false
	}

	delete(w.players, playerID)
	delete(w.movedPlayerIDs, playerID)
	w.removedPlayerIDs[playerID] = struct{}{}
	return true
}

func (w *World) ResetInput(playerID int64) {
	p, exists := w.players[playerID]
	if !exists {
		return
	}
	p.input = InputState{}
	p.lastSequence = 0
}

func (w *World) ApplyInput(playerID int64, input InputState) bool {
	p, exists := w.players[playerID]
	if !exists || input.Sequence <= p.lastSequence {
		return false
	}

	p.input = input
	p.lastSequence = input.Sequence
	return true
}

func (w *World) Step(deltaTime time.Duration) []int64 {
	w.tick += 1
	distance := w.config.SpeedMetersPerSecond * deltaTime.Seconds()
	moved := make([]int64, 0)

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
		w.movedPlayerIDs[playerID] = struct{}{}
		moved = append(moved, playerID)
	}
	sort.Slice(moved, func(i, j int) bool { return moved[i] < moved[j] })
	return moved
}

func (w *World) PlayerIDs() []int64 {
	return w.playersKeys()
}

func (w *World) PlayerState(playerID int64) (PlayerState, bool) {
	p, exists := w.players[playerID]
	if !exists {
		return PlayerState{}, false
	}
	return w.playerState(p), true
}

func (w *World) PlayerStates(playerIDs []int64) []PlayerState {
	return w.statesFor(playerIDs)
}

func (w *World) PlayerPosition(playerID int64) (PlayerPosition, bool) {
	p, exists := w.players[playerID]
	if !exists {
		return PlayerPosition{}, false
	}
	return p.position, true
}

func (w *World) PlayerPositions(playerIDs []int64) []PlayerPosition {
	positions := make([]PlayerPosition, 0, len(playerIDs))
	for _, id := range playerIDs {
		if p, exists := w.players[id]; exists {
			positions = append(positions, p.position)
		}
	}
	return positions
}

func (w *World) PlayerAppearance(playerID int64) (Appearance, bool) {
	p, exists := w.players[playerID]
	if !exists {
		return Appearance{}, false
	}
	return p.appearance, true
}

func (w *World) UpdatePlayerAppearance(playerID int64, appearance Appearance) (changed bool, ok bool) {
	p, exists := w.players[playerID]
	if !exists {
		return false, false
	}
	if p.appearance == appearance {
		return false, true
	}
	p.appearance = appearance
	return true, true
}

func (w *World) TakeMovedPlayerIDs() []int64 {
	ids := setKeys(w.movedPlayerIDs)
	clear(w.movedPlayerIDs)
	return ids
}

func (w *World) TakeRemovedPlayerIDs() []int64 {
	ids := setKeys(w.removedPlayerIDs)
	clear(w.removedPlayerIDs)
	return ids
}

func (w *World) PlayerCount() int {
	return len(w.players)
}

func (w *World) playersKeys() []int64 {
	ids := make([]int64, 0, len(w.players))
	for id := range w.players {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (w *World) playerState(p *player) PlayerState {
	return PlayerState{
		ID:         p.position.ID,
		Username:   p.username,
		Lat:        p.position.Lat,
		Lng:        p.position.Lng,
		Appearance: p.appearance,
	}
}

func (w *World) statesFor(ids []int64) []PlayerState {
	states := make([]PlayerState, 0, len(ids))
	for _, id := range ids {
		if p, exists := w.players[id]; exists {
			states = append(states, w.playerState(p))
		}
	}
	return states
}

func setKeys(values map[int64]struct{}) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
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
