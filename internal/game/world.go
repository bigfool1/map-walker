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

type PlayerState struct {
	ID         string     `json:"id"`
	Username   string     `json:"username"`
	Lat        float64    `json:"lat"`
	Lng        float64    `json:"lng"`
	Appearance Appearance `json:"appearance"`
}

type Snapshot struct {
	Tick    uint64
	Players []PlayerState
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
	username     string
	appearance   Appearance
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

func (w *World) SpawnLatLng() (float64, float64) {
	return w.config.SpawnLat, w.config.SpawnLng
}

func (w *World) AddPlayer(playerID string) bool {
	return w.AddPlayerWithState(playerID, playerID, w.config.SpawnLat, w.config.SpawnLng, DefaultAppearance())
}

func (w *World) AddPlayerAt(playerID string, lat, lng float64) bool {
	return w.AddPlayerWithState(playerID, playerID, lat, lng, DefaultAppearance())
}

func (w *World) AddPlayerWithAppearance(playerID string, lat, lng float64, appearance Appearance) bool {
	return w.AddPlayerWithState(playerID, playerID, lat, lng, appearance)
}

func (w *World) AddPlayerWithState(playerID, username string, lat, lng float64, appearance Appearance) bool {
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
	w.dirtyPlayerIDs[playerID] = struct{}{}
	delete(w.removedPlayerIDs, playerID)
	return true
}

func (w *World) HasPlayer(playerID string) bool {
	_, exists := w.players[playerID]
	return exists
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

func (w *World) Step(deltaTime time.Duration) []string {
	w.tick += 1
	distance := w.config.SpeedMetersPerSecond * deltaTime.Seconds()
	moved := make([]string, 0)

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
		moved = append(moved, playerID)
	}
	sort.Strings(moved)
	return moved
}

func (w *World) PlayerPosition(playerID string) (PlayerPosition, bool) {
	p, exists := w.players[playerID]
	if !exists {
		return PlayerPosition{}, false
	}
	return p.position, true
}

func (w *World) PlayerAppearance(playerID string) (Appearance, bool) {
	p, exists := w.players[playerID]
	if !exists {
		return Appearance{}, false
	}
	return p.appearance, true
}

func (w *World) UpdatePlayerAppearance(playerID string, appearance Appearance) (changed bool, ok bool) {
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

func (w *World) Snapshot() Snapshot {
	return Snapshot{
		Tick:    w.tick,
		Players: w.statesFor(w.playersKeys()),
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

func (w *World) statesFor(ids []string) []PlayerState {
	states := make([]PlayerState, 0, len(ids))
	for _, id := range ids {
		if p, exists := w.players[id]; exists {
			states = append(states, PlayerState{
				ID:         p.position.ID,
				Username:   p.username,
				Lat:        p.position.Lat,
				Lng:        p.position.Lng,
				Appearance: p.appearance,
			})
		}
	}
	return states
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
