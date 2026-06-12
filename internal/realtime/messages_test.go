package realtime

import (
	"encoding/json"
	"testing"

	"map-walker/internal/game"
)

func TestDecodeInputMessageIgnoresCoordinates(t *testing.T) {
	raw := []byte(`{
		"type":"input",
		"sequence":42,
		"up":true,
		"down":false,
		"left":false,
		"right":true,
		"lat":0,
		"lng":0
	}`)

	var message InputMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if message.Type != MessageTypeInput || message.Sequence != 42 {
		t.Fatalf("unexpected input metadata: %+v", message)
	}
	if !message.Up || message.Down || message.Left || !message.Right {
		t.Fatalf("unexpected directions: %+v", message)
	}

	input := message.InputState()
	if input != (game.InputState{Sequence: 42, Up: true, Right: true}) {
		t.Fatalf("unexpected game input: %+v", input)
	}
}

func TestEncodeWorldSnapshot(t *testing.T) {
	data, err := EncodeWorldSnapshot(game.Snapshot{
		Tick: 7,
		Players: []game.PlayerState{
			{
				ID:         "alice",
				Username:   "Alice",
				Lat:        31.2304,
				Lng:        121.4737,
				Appearance: game.Appearance{Color: "#3388ff", Shape: game.ShapeCircle},
			},
		},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"world_snapshot","tick":7,"players":[{"id":"alice","username":"Alice","lat":31.2304,"lng":121.4737,"appearance":{"color":"#3388ff","shape":"circle"}}]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeAppearanceChanged(t *testing.T) {
	data, err := EncodeAppearanceChanged("user-id", game.Appearance{
		Color: "#ff6600",
		Shape: game.ShapeDiamond,
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"appearance_changed","playerId":"user-id","appearance":{"color":"#ff6600","shape":"diamond"}}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodePlayersDelta(t *testing.T) {
	data, err := EncodePlayersDelta(game.Delta{
		Tick: 9,
		Players: []game.PlayerState{
			{ID: "alice", Username: "alice", Lat: 31.2305, Lng: 121.4738, Appearance: game.DefaultAppearance()},
		},
		RemovedPlayerIDs: []string{"bob"},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"players_delta","tick":9,"players":[{"id":"alice","username":"alice","lat":31.2305,"lng":121.4738,"appearance":{"color":"#3388ff","shape":"circle"}}],"removedPlayerIds":["bob"]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}
