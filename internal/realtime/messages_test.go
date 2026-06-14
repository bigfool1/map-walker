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
		ID:         1001,
		Username:   "Alice",
		Lat:        31.2304,
		Lng:        121.4737,
		Appearance: game.Appearance{Color: "#3388ff", Shape: game.ShapeCircle},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"self_state","tick":42,"player":{"id":1001,"username":"Alice","lat":31.2304,"lng":121.4737,"appearance":{"color":"#3388ff","shape":"circle"}}}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeVisibleEntitiesSnapshot(t *testing.T) {
	data, err := EncodeVisibleEntitiesSnapshot(42, []game.PlayerState{
		{
			ID:         1002,
			Username:   "Bob",
			Lat:        31.2308,
			Lng:        121.4737,
			Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond},
		},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"visible_entities_snapshot","tick":42,"players":[{"id":1002,"username":"Bob","lat":31.2308,"lng":121.4737,"appearance":{"color":"#ff6600","shape":"diamond"}}]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeVisibleEntitiesSnapshotSortsByPlayerID(t *testing.T) {
	players := []game.PlayerState{
		{ID: 3003, Username: "Charlie", Lat: 31.2310, Lng: 121.4739, Appearance: game.Appearance{Color: "#112233", Shape: game.ShapeSquare}},
		{ID: 3001, Username: "Alice", Lat: 31.2304, Lng: 121.4737, Appearance: game.Appearance{Color: "#3388ff", Shape: game.ShapeCircle}},
		{ID: 3002, Username: "Bob", Lat: 31.2308, Lng: 121.4737, Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}},
	}

	data, err := EncodeVisibleEntitiesSnapshot(42, players)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"visible_entities_snapshot","tick":42,"players":[{"id":3001,"username":"Alice","lat":31.2304,"lng":121.4737,"appearance":{"color":"#3388ff","shape":"circle"}},{"id":3002,"username":"Bob","lat":31.2308,"lng":121.4737,"appearance":{"color":"#ff6600","shape":"diamond"}},{"id":3003,"username":"Charlie","lat":31.231,"lng":121.4739,"appearance":{"color":"#112233","shape":"square"}}]}`
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
	data, err := EncodeReplicationUpdate(44, 1001, ReplicationChanges{
		SelfPosition: &SelfPosition{Lat: 31.2305, Lng: 121.4737},
		Entered: []game.PlayerState{
			{ID: 4001, Username: "Carol", Lat: 31.2310, Lng: 121.4738, Appearance: game.Appearance{Color: "#00aa66", Shape: game.ShapeTriangle}},
		},
		LeftPlayerIDs: []int64{4002},
		Positions:     []game.PlayerPosition{{ID: 4003, Lat: 31.2307, Lng: 121.4739}},
		Appearances:   []PlayerAppearanceUpdate{{PlayerID: 4003, Appearance: game.Appearance{Color: "#8844ff", Shape: game.ShapeSquare}}},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"replication_update","tick":44,"selfPosition":{"lat":31.2305,"lng":121.4737},"entered":[{"id":4001,"username":"Carol","lat":31.231,"lng":121.4738,"appearance":{"color":"#00aa66","shape":"triangle"}}],"leftPlayerIds":[4002],"positions":[{"id":4003,"lat":31.2307,"lng":121.4739}],"appearances":[{"playerId":4003,"appearance":{"color":"#8844ff","shape":"square"}}]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeReplicationUpdateOmitsEmptyOptionalFields(t *testing.T) {
	data, err := EncodeReplicationUpdate(10, 1001, ReplicationChanges{
		Positions: []game.PlayerPosition{{ID: 5001, Lat: 31.2306, Lng: 121.4738}},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	want := `{"type":"replication_update","tick":10,"positions":[{"id":5001,"lat":31.2306,"lng":121.4738}]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeReplicationUpdateSkipsEmptyUpdate(t *testing.T) {
	_, err := EncodeReplicationUpdate(10, 1001, ReplicationChanges{})
	if !errors.Is(err, ErrEmptyReplicationUpdate) {
		t.Fatalf("expected ErrEmptyReplicationUpdate, got %v", err)
	}
	data, ok, err := TryEncodeReplicationUpdate(10, 1001, ReplicationChanges{})
	if err != nil || ok || data != nil {
		t.Fatalf("expected skipped, ok=%v data=%q err=%v", ok, data, err)
	}
}

func TestNormalizeReplicationPrecedenceLeftWins(t *testing.T) {
	normalized := NormalizeReplicationChanges(1001, ReplicationChanges{
		LeftPlayerIDs: []int64{6001},
		Entered:       []game.PlayerState{{ID: 6001, Username: "Bob", Lat: 31.1, Lng: 121.1, Appearance: game.DefaultAppearance()}},
		Positions:     []game.PlayerPosition{{ID: 6001, Lat: 31.2, Lng: 121.2}},
		Appearances:   []PlayerAppearanceUpdate{{PlayerID: 6001, Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}}},
	})
	if !slicesEqualInt64(normalized.LeftPlayerIDs, []int64{6001}) {
		t.Fatalf("left = %+v, want [6001]", normalized.LeftPlayerIDs)
	}
	if len(normalized.Entered) != 0 || len(normalized.Positions) != 0 || len(normalized.Appearances) != 0 {
		t.Fatalf("expected left to suppress other public fields, got %+v", normalized)
	}
}

func TestNormalizeReplicationPrecedenceEnteredExcludesPositionsAndAppearances(t *testing.T) {
	normalized := NormalizeReplicationChanges(1001, ReplicationChanges{
		Entered:     []game.PlayerState{{ID: 6001, Username: "Bob", Lat: 31.1, Lng: 121.1, Appearance: game.DefaultAppearance()}},
		Positions:   []game.PlayerPosition{{ID: 6001, Lat: 31.2, Lng: 121.2}},
		Appearances: []PlayerAppearanceUpdate{{PlayerID: 6001, Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}}},
	})
	if len(normalized.Entered) != 1 || normalized.Entered[0].ID != 6001 {
		t.Fatalf("entered = %+v", normalized.Entered)
	}
	if len(normalized.Positions) != 0 || len(normalized.Appearances) != 0 {
		t.Fatalf("entered should exclude positions and appearances, got %+v", normalized)
	}
}

func TestNormalizeReplicationAllowsPositionAndAppearanceTogether(t *testing.T) {
	normalized := NormalizeReplicationChanges(1001, ReplicationChanges{
		Positions:   []game.PlayerPosition{{ID: 6001, Lat: 31.2, Lng: 121.2}},
		Appearances: []PlayerAppearanceUpdate{{PlayerID: 6001, Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}}},
	})
	if len(normalized.Positions) != 1 || len(normalized.Appearances) != 1 {
		t.Fatalf("expected both position and appearance, got %+v", normalized)
	}
}

func TestNormalizeReplicationExcludesSelfFromPublicFields(t *testing.T) {
	normalized := NormalizeReplicationChanges(1001, ReplicationChanges{
		Entered: []game.PlayerState{
			{ID: 1001, Username: "Self", Lat: 31.1, Lng: 121.1, Appearance: game.DefaultAppearance()},
			{ID: 6001, Username: "Bob", Lat: 31.2, Lng: 121.2, Appearance: game.DefaultAppearance()},
		},
		LeftPlayerIDs: []int64{7001},
		Positions:     []game.PlayerPosition{{ID: 1001, Lat: 31.3, Lng: 121.3}},
		Appearances:   []PlayerAppearanceUpdate{{PlayerID: 1001, Appearance: game.Appearance{Color: "#112233", Shape: game.ShapeSquare}}},
	})
	if len(normalized.Entered) != 1 || normalized.Entered[0].ID != 6001 {
		t.Fatalf("entered = %+v, want only bob", normalized.Entered)
	}
	if !slicesEqualInt64(normalized.LeftPlayerIDs, []int64{7001}) {
		t.Fatalf("left = %+v", normalized.LeftPlayerIDs)
	}
	if len(normalized.Appearances) != 1 || normalized.Appearances[0].PlayerID != 1001 {
		t.Fatalf("self appearance should remain, got %+v", normalized.Appearances)
	}
}

func TestNormalizeReplicationCollapsesRepeatedAppearancesToFinalValue(t *testing.T) {
	normalized := NormalizeReplicationChanges(1001, ReplicationChanges{
		Appearances: []PlayerAppearanceUpdate{
			{PlayerID: 6001, Appearance: game.Appearance{Color: "#111111", Shape: game.ShapeCircle}},
			{PlayerID: 6001, Appearance: game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}},
		},
	})
	if len(normalized.Appearances) != 1 || normalized.Appearances[0].Appearance.Color != "#ff6600" {
		t.Fatalf("expected final appearance, got %+v", normalized.Appearances)
	}
}

func slicesEqualInt64(a, b []int64) bool {
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
