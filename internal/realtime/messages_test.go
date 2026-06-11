package realtime

import (
	"encoding/json"
	"testing"

	"map-walker/internal/game"
)

func TestDecodePositionUpdate(t *testing.T) {
	raw := []byte(`{"type":"position_update","playerId":"alice","lat":31.2304,"lng":121.4737}`)

	var msg PositionUpdateMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if msg.Type != MessageTypePositionUpdate {
		t.Fatalf("unexpected type: %q", msg.Type)
	}
	if msg.PlayerID != "alice" || msg.Lat != 31.2304 || msg.Lng != 121.4737 {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestEncodePlayersSnapshot(t *testing.T) {
	msg := PlayersSnapshotMessage{
		Type: MessageTypePlayersSnapshot,
		Players: []game.PlayerPosition{
			{ID: "alice", Lat: 31.2304, Lng: 121.4737},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"players_snapshot","players":[{"id":"alice","lat":31.2304,"lng":121.4737}]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}
