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
	}, 42)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"self_state","tick":42,"player":{"id":1001,"username":"Alice","lat":31.2304,"lng":121.4737,"appearance":{"color":"#3388ff","shape":"circle"}},"score":42}`
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

func TestDecodeCollectMessage(t *testing.T) {
	raw := []byte(`{"type":"collect","collectibleId":123}`)
	var msg CollectMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("decode collect failed: %v", err)
	}
	if msg.Type != MessageTypeCollect || msg.CollectibleID != 123 {
		t.Fatalf("unexpected collect message: %+v", msg)
	}
}

func TestEncodeCollectResult(t *testing.T) {
	data, err := EncodeCollectResult(123, 42)
	if err != nil {
		t.Fatalf("encode collect_result failed: %v", err)
	}
	want := `{"type":"collect_result","collectibleId":123,"score":42}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodeCollectibleRegions(t *testing.T) {
	regions := []game.CollectibleRegion{
		{ID: "region-1", CenterLat: 31.2304, CenterLng: 121.4737, RadiusMeters: 200},
		{ID: "region-2", CenterLat: 31.2350, CenterLng: 121.4780, RadiusMeters: 200},
	}
	data, err := EncodeCollectibleRegions(42, regions)
	if err != nil {
		t.Fatalf("encode collectible_regions failed: %v", err)
	}

	var msg CollectibleRegionsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode collectible_regions failed: %v", err)
	}
	if msg.Type != MessageTypeCollectibleRegions || msg.Tick != 42 {
		t.Fatalf("unexpected metadata: %+v", msg)
	}
	if len(msg.Regions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(msg.Regions))
	}
	if msg.Regions[0].ID != "region-1" {
		t.Fatalf("region[0].ID = %s", msg.Regions[0].ID)
	}
	// 验证不暴露 targetCount 和 respawn
	if msg.Regions[0].CenterLat != 31.2304 {
		t.Fatalf("regions[0].CenterLat mismatch")
	}
}

func TestEncodeVisibleCollectiblesSnapshot(t *testing.T) {
	collectibles := []game.Collectible{
		{ID: 1, RegionID: "region-1", Lat: 31.2305, Lng: 121.4738},
		{ID: 2, RegionID: "region-1", Lat: 31.2306, Lng: 121.4739},
	}
	data, err := EncodeVisibleCollectiblesSnapshot(42, collectibles)
	if err != nil {
		t.Fatalf("encode snapshot failed: %v", err)
	}

	var msg VisibleCollectiblesSnapshotMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode snapshot failed: %v", err)
	}
	if len(msg.Collectibles) != 2 {
		t.Fatalf("expected 2 collectibles, got %d", len(msg.Collectibles))
	}
	// 验证按 ID 排序
	if msg.Collectibles[0].ID != 1 || msg.Collectibles[1].ID != 2 {
		t.Fatalf("collectibles not sorted by ID: %+v", msg.Collectibles)
	}
}

func TestEncodeVisibleCollectiblesSnapshotEmpty(t *testing.T) {
	data, err := EncodeVisibleCollectiblesSnapshot(42, nil)
	if err != nil {
		t.Fatalf("encode empty snapshot failed: %v", err)
	}
	var msg VisibleCollectiblesSnapshotMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode empty snapshot failed: %v", err)
	}
	if len(msg.Collectibles) != 0 {
		t.Fatalf("expected empty collectibles, got %d", len(msg.Collectibles))
	}
}

func TestReplicationCollectibleEnteredAndLeft(t *testing.T) {
	changes := ReplicationChanges{
		CollectiblesEntered: []CollectibleEnteredItem{
			{ID: 10, RegionID: "region-1", Lat: 31.23, Lng: 121.47},
		},
		CollectibleIDsLeft: []uint64{5, 6},
	}
	if changes.IsEmpty() {
		t.Fatal("expected non-empty changes")
	}

	normalized := NormalizeReplicationChanges(1001, changes)
	if len(normalized.CollectiblesEntered) != 1 {
		t.Fatalf("entered = %d, want 1", len(normalized.CollectiblesEntered))
	}
	if len(normalized.CollectibleIDsLeft) != 2 {
		t.Fatalf("left = %d, want 2", len(normalized.CollectibleIDsLeft))
	}

	data, err := EncodeReplicationUpdate(42, 1001, changes)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	var msg ReplicationUpdateMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(msg.CollectiblesEntered) != 1 {
		t.Fatalf("JSON entered = %d, want 1", len(msg.CollectiblesEntered))
	}
}

func TestReplicationCollectibleContradictionNormalization(t *testing.T) {
	// 同一 collectible 不能同时 entered 和 left
	changes := ReplicationChanges{
		CollectiblesEntered: []CollectibleEnteredItem{
			{ID: 10, RegionID: "region-1", Lat: 31.23, Lng: 121.47},
		},
		CollectibleIDsLeft: []uint64{10},
	}
	normalized := NormalizeReplicationChanges(1001, changes)
	if len(normalized.CollectiblesEntered) != 0 {
		t.Fatalf("contradictory entered should be empty, got %d", len(normalized.CollectiblesEntered))
	}
	if len(normalized.CollectibleIDsLeft) != 1 {
		t.Fatalf("left should remain, got %d", len(normalized.CollectibleIDsLeft))
	}
}

func TestReplicationCollectibleSpawnedAndCollectedContradiction(t *testing.T) {
	// spawned 和 collected 同时出现：collected 优先，移除 spawned 中矛盾项
	changes := ReplicationChanges{
		CollectiblesSpawned: []CollectibleSpawnedItem{
			{ID: 20, RegionID: "region-1", Lat: 31.23, Lng: 121.47},
		},
		CollectibleIDsCollected: []uint64{20},
	}
	normalized := NormalizeReplicationChanges(1001, changes)
	if len(normalized.CollectiblesSpawned) != 0 {
		t.Fatalf("spawned should be filtered when also collected")
	}
	if len(normalized.CollectibleIDsCollected) != 1 {
		t.Fatalf("collected should remain after normalization")
	}
}

func TestReplicationEmptyCollectibleChanges(t *testing.T) {
	changes := ReplicationChanges{}
	if !changes.IsEmpty() {
		t.Fatal("empty changes should be empty")
	}
	_, ok, err := TryEncodeReplicationUpdate(42, 1001, changes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("empty changes should not produce update")
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
