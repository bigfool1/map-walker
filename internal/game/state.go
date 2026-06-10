package game

import "sort"

// PlayerPosition is the tiny piece of game state this MVP synchronizes.
// In a larger game this would likely grow into player movement state, avatar
// state, animation state, or room/world membership.
type PlayerPosition struct {
	ID  string  `json:"id"`
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type State struct {
	players map[string]PlayerPosition
}

func NewState() *State {
	return &State{
		players: map[string]PlayerPosition{},
	}
}

func (s *State) UpdatePosition(position PlayerPosition) {
	s.players[position.ID] = position
}

func (s *State) RemovePlayer(playerID string) {
	delete(s.players, playerID)
}

func (s *State) Snapshot() []PlayerPosition {
	players := make([]PlayerPosition, 0, len(s.players))
	for _, player := range s.players {
		players = append(players, player)
	}

	sort.Slice(players, func(i, j int) bool {
		return players[i].ID < players[j].ID
	})

	return players
}
