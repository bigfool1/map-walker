package realtime

import (
	"encoding/json"

	"map-walker/internal/game"
)

const (
	MessageTypeInput         = "input"
	MessageTypeWorldSnapshot = "world_snapshot"
	MessageTypePlayersDelta  = "players_delta"
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
	Type    string                `json:"type"`
	Tick    uint64                `json:"tick"`
	Players []game.PlayerPosition `json:"players"`
}

type PlayersDeltaMessage struct {
	Type             string                `json:"type"`
	Tick             uint64                `json:"tick"`
	Players          []game.PlayerPosition `json:"players"`
	RemovedPlayerIDs []string              `json:"removedPlayerIds"`
}

func EncodeWorldSnapshot(snapshot game.Snapshot) ([]byte, error) {
	return json.Marshal(WorldSnapshotMessage{
		Type:    MessageTypeWorldSnapshot,
		Tick:    snapshot.Tick,
		Players: snapshot.Players,
	})
}

func EncodePlayersDelta(delta game.Delta) ([]byte, error) {
	return json.Marshal(PlayersDeltaMessage{
		Type:             MessageTypePlayersDelta,
		Tick:             delta.Tick,
		Players:          delta.Players,
		RemovedPlayerIDs: delta.RemovedPlayerIDs,
	})
}
