package realtime

import (
	"encoding/json"

	"map-walker/internal/game"
)

const (
	MessageTypeInput             = "input"
	MessageTypeWorldSnapshot     = "world_snapshot"
	MessageTypePlayersDelta      = "players_delta"
	MessageTypeAppearanceChanged = "appearance_changed"
)

type InputMessage struct {
	Type     string `json:"type"`
	Sequence uint64 `json:"sequence"`
	Up       bool   `json:"up"`
	Down     bool   `json:"down"`
	Left     bool   `json:"left"`
	Right    bool   `json:"right"`
}

func (m InputMessage) InputState() game.InputState {
	return game.InputState{
		Sequence: m.Sequence,
		Up:       m.Up,
		Down:     m.Down,
		Left:     m.Left,
		Right:    m.Right,
	}
}

type WorldSnapshotMessage struct {
	Type    string            `json:"type"`
	Tick    uint64            `json:"tick"`
	Players []game.PlayerState `json:"players"`
}

type AppearanceChangedMessage struct {
	Type       string         `json:"type"`
	PlayerID   string         `json:"playerId"`
	Appearance game.Appearance `json:"appearance"`
}

type PlayersDeltaMessage struct {
	Type             string              `json:"type"`
	Tick             uint64              `json:"tick"`
	Players          []game.PlayerState  `json:"players"`
	RemovedPlayerIDs []string            `json:"removedPlayerIds"`
}

func EncodeWorldSnapshot(tick uint64, players []game.PlayerState) ([]byte, error) {
	return json.Marshal(WorldSnapshotMessage{
		Type:    MessageTypeWorldSnapshot,
		Tick:    tick,
		Players: players,
	})
}

func EncodePlayersDelta(tick uint64, players []game.PlayerState, removedPlayerIDs []string) ([]byte, error) {
	return json.Marshal(PlayersDeltaMessage{
		Type:             MessageTypePlayersDelta,
		Tick:             tick,
		Players:          players,
		RemovedPlayerIDs: removedPlayerIDs,
	})
}

func EncodeAppearanceChanged(playerID string, appearance game.Appearance) ([]byte, error) {
	return json.Marshal(AppearanceChangedMessage{
		Type:       MessageTypeAppearanceChanged,
		PlayerID:   playerID,
		Appearance: appearance,
	})
}
