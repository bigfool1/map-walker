package realtime

import "map-walker/internal/game"

const (
	MessageTypePositionUpdate  = "position_update"
	MessageTypePlayersSnapshot = "players_snapshot"
)

type PositionUpdateMessage struct {
	Type     string  `json:"type"`
	PlayerID string  `json:"playerId"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
}

type PlayersSnapshotMessage struct {
	Type    string                `json:"type"`
	Players []game.PlayerPosition `json:"players"`
}

func NewPlayersSnapshotMessage(players []game.PlayerPosition) PlayersSnapshotMessage {
	return PlayersSnapshotMessage{
		Type:    MessageTypePlayersSnapshot,
		Players: players,
	}
}
