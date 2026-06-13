package realtime

import (
	"encoding/json"
	"errors"
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

func TestEncodeSelfState(t *testing.T) {
	data, err := EncodeSelfState(42, game.PlayerState{
		ID:         "self-id",
		Username:   "Alice",
		Lat:        31.2304,
		Lng:        121.4737,
		Appearance: game.Appearance{Color: "#3388ff", Shape: game.ShapeCircle},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"self_state","tick":42,"player":{"id":"self-id","username":"Alice","lat":31.2304,"lng":121.4737,"appearance":{"color":"#3388ff","shape":"circle"}}}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeVisibleEntitiesSnapshot(t *testing.T) {
	data, err := EncodeVisibleEntitiesSnapshot(42, []game.PlayerState{
		{
			ID:         "other-id",
			Username:   "Bob",
			Lat:        31.2308,
			Lng:        121.4737,
			Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond},
		},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"visible_entities_snapshot","tick":42,"players":[{"id":"other-id","username":"Bob","lat":31.2308,"lng":121.4737,"appearance":{"color":"#ff6600","shape":"diamond"}}]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeVisibleEntitiesSnapshotEmptyPlayers(t *testing.T) {
	data, err := EncodeVisibleEntitiesSnapshot(7, nil)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"visible_entities_snapshot","tick":7,"players":[]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeReplicationUpdateFullPayload(t *testing.T) {
	data, err := EncodeReplicationUpdate(44, "self-id", ReplicationChanges{
		SelfPosition: &SelfPosition{Lat: 31.2305, Lng: 121.4737},
		Entered: []game.PlayerState{
			{
				ID:         "entered-id",
				Username:   "Carol",
				Lat:        31.2310,
				Lng:        121.4738,
				Appearance: game.Appearance{Color: "#00aa66", Shape: game.ShapeTriangle},
			},
		},
		LeftPlayerIDs: []string{"left-id"},
		Positions: []game.PlayerPosition{
			{ID: "visible-id", Lat: 31.2307, Lng: 121.4739},
		},
		Appearances: []PlayerAppearanceUpdate{
			{
				PlayerID:   "visible-id",
				Appearance: game.Appearance{Color: "#8844ff", Shape: game.ShapeSquare},
			},
		},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"replication_update","tick":44,"selfPosition":{"lat":31.2305,"lng":121.4737},"entered":[{"id":"entered-id","username":"Carol","lat":31.231,"lng":121.4738,"appearance":{"color":"#00aa66","shape":"triangle"}}],"leftPlayerIds":["left-id"],"positions":[{"id":"visible-id","lat":31.2307,"lng":121.4739}],"appearances":[{"playerId":"visible-id","appearance":{"color":"#8844ff","shape":"square"}}]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeReplicationUpdateOmitsEmptyOptionalFields(t *testing.T) {
	data, err := EncodeReplicationUpdate(10, "self-id", ReplicationChanges{
		Positions: []game.PlayerPosition{
			{ID: "bob", Lat: 31.2306, Lng: 121.4738},
		},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"replication_update","tick":10,"positions":[{"id":"bob","lat":31.2306,"lng":121.4738}]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeReplicationUpdateSkipsEmptyUpdate(t *testing.T) {
	_, err := EncodeReplicationUpdate(10, "self-id", ReplicationChanges{})
	if !errors.Is(err, ErrEmptyReplicationUpdate) {
		t.Fatalf("expected ErrEmptyReplicationUpdate, got %v", err)
	}

	data, ok, err := TryEncodeReplicationUpdate(10, "self-id", ReplicationChanges{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || data != nil {
		t.Fatalf("expected skipped empty update, ok=%v data=%q", ok, data)
	}
}

func TestNormalizeReplicationPrecedenceLeftWins(t *testing.T) {
	normalized := NormalizeReplicationChanges("self-id", ReplicationChanges{
		LeftPlayerIDs: []string{"bob"},
		Entered: []game.PlayerState{
			{ID: "bob", Username: "Bob", Lat: 31.1, Lng: 121.1, Appearance: game.DefaultAppearance()},
		},
		Positions: []game.PlayerPosition{
			{ID: "bob", Lat: 31.2, Lng: 121.2},
		},
		Appearances: []PlayerAppearanceUpdate{
			{PlayerID: "bob", Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}},
		},
	})

	if !slicesEqual(normalized.LeftPlayerIDs, []string{"bob"}) {
		t.Fatalf("left = %+v, want [bob]", normalized.LeftPlayerIDs)
	}
	if len(normalized.Entered) != 0 || len(normalized.Positions) != 0 || len(normalized.Appearances) != 0 {
		t.Fatalf("expected left to suppress other public fields, got %+v", normalized)
	}
}

func TestNormalizeReplicationPrecedenceEnteredExcludesPositionsAndAppearances(t *testing.T) {
	normalized := NormalizeReplicationChanges("self-id", ReplicationChanges{
		Entered: []game.PlayerState{
			{ID: "bob", Username: "Bob", Lat: 31.1, Lng: 121.1, Appearance: game.DefaultAppearance()},
		},
		Positions: []game.PlayerPosition{
			{ID: "bob", Lat: 31.2, Lng: 121.2},
		},
		Appearances: []PlayerAppearanceUpdate{
			{PlayerID: "bob", Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}},
		},
	})

	if len(normalized.Entered) != 1 || normalized.Entered[0].ID != "bob" {
		t.Fatalf("entered = %+v", normalized.Entered)
	}
	if len(normalized.Positions) != 0 || len(normalized.Appearances) != 0 {
		t.Fatalf("entered should exclude positions and appearances, got %+v", normalized)
	}
}

func TestNormalizeReplicationAllowsPositionAndAppearanceTogether(t *testing.T) {
	normalized := NormalizeReplicationChanges("self-id", ReplicationChanges{
		Positions: []game.PlayerPosition{
			{ID: "bob", Lat: 31.2, Lng: 121.2},
		},
		Appearances: []PlayerAppearanceUpdate{
			{PlayerID: "bob", Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}},
		},
	})

	if len(normalized.Positions) != 1 || len(normalized.Appearances) != 1 {
		t.Fatalf("expected both position and appearance, got %+v", normalized)
	}
}

func TestNormalizeReplicationExcludesSelfFromPublicFields(t *testing.T) {
	normalized := NormalizeReplicationChanges("self-id", ReplicationChanges{
		Entered: []game.PlayerState{
			{ID: "self-id", Username: "Self", Lat: 31.1, Lng: 121.1, Appearance: game.DefaultAppearance()},
			{ID: "bob", Username: "Bob", Lat: 31.2, Lng: 121.2, Appearance: game.DefaultAppearance()},
		},
		LeftPlayerIDs: []string{"carol"},
		Positions: []game.PlayerPosition{
			{ID: "self-id", Lat: 31.3, Lng: 121.3},
		},
		Appearances: []PlayerAppearanceUpdate{
			{PlayerID: "self-id", Appearance: game.Appearance{Color: "#112233", Shape: game.ShapeSquare}},
		},
	})

	if len(normalized.Entered) != 1 || normalized.Entered[0].ID != "bob" {
		t.Fatalf("entered = %+v, want only bob", normalized.Entered)
	}
	if !slicesEqual(normalized.LeftPlayerIDs, []string{"carol"}) {
		t.Fatalf("left = %+v", normalized.LeftPlayerIDs)
	}
	if len(normalized.Positions) != 0 {
		t.Fatalf("positions = %+v, want none when bob is entered", normalized.Positions)
	}
	if len(normalized.Appearances) != 1 || normalized.Appearances[0].PlayerID != "self-id" {
		t.Fatalf("self appearance should remain, got %+v", normalized.Appearances)
	}
}

func TestNormalizeReplicationCollapsesRepeatedAppearancesToFinalValue(t *testing.T) {
	normalized := NormalizeReplicationChanges("self-id", ReplicationChanges{
		Appearances: []PlayerAppearanceUpdate{
			{PlayerID: "bob", Appearance: game.Appearance{Color: "#111111", Shape: game.ShapeCircle}},
			{PlayerID: "bob", Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}},
		},
	})

	if len(normalized.Appearances) != 1 {
		t.Fatalf("expected one appearance, got %+v", normalized.Appearances)
	}
	if normalized.Appearances[0].Appearance.Color != "#ff6600" || normalized.Appearances[0].Appearance.Shape != game.ShapeDiamond {
		t.Fatalf("expected final appearance, got %+v", normalized.Appearances[0])
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
