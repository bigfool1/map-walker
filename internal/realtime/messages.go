package realtime

import (
	"encoding/json"
	"errors"
	"sort"

	"map-walker/internal/game"
)

var ErrEmptyReplicationUpdate = errors.New("empty replication update")

const (
	MessageTypeInput                        = "input"
	MessageTypeSelfState                    = "self_state"
	MessageTypeVisibleEntitiesSnapshot      = "visible_entities_snapshot"
	MessageTypeReplicationUpdate            = "replication_update"
	MessageTypeCollect                      = "collect"
	MessageTypeCollectResult                = "collect_result"
	MessageTypeCollectibleRegions           = "collectible_regions"
	MessageTypeVisibleCollectiblesSnapshot  = "visible_collectibles_snapshot"
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

// CollectMessage 客户端拾取意图
type CollectMessage struct {
	Type          string `json:"type"`
	CollectibleID uint64 `json:"collectibleId"`
}

// CollectResultMessage 服务端拾取成功响应（仅发送给获胜者）
type CollectResultMessage struct {
	Type          string `json:"type"`
	CollectibleID uint64 `json:"collectibleId"`
	Score         int64  `json:"score"`
}

type SelfStateMessage struct {
	Type   string           `json:"type"`
	Tick   uint64           `json:"tick"`
	Player game.PlayerState `json:"player"`
	Score  int64            `json:"score"`
}

type VisibleEntitiesSnapshotMessage struct {
	Type    string             `json:"type"`
	Tick    uint64             `json:"tick"`
	Players []game.PlayerState `json:"players"`
}

// CollectibleRegionPublic 客户端可见的区域公开几何信息
type CollectibleRegionPublic struct {
	ID           string  `json:"id"`
	CenterLat    float64 `json:"centerLat"`
	CenterLng    float64 `json:"centerLng"`
	RadiusMeters float64 `json:"radiusMeters"`
}

// CollectibleRegionsMessage 连接时发送的区域几何信息
type CollectibleRegionsMessage struct {
	Type    string                    `json:"type"`
	Tick    uint64                    `json:"tick"`
	Regions []CollectibleRegionPublic `json:"regions"`
}

// CollectibleSnapshotItem 收集品快照条目
type CollectibleSnapshotItem struct {
	ID       uint64  `json:"id"`
	RegionID string  `json:"regionId"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
}

// VisibleCollectiblesSnapshotMessage 连接时发送的可见收集品快照
type VisibleCollectiblesSnapshotMessage struct {
	Type         string                    `json:"type"`
	Tick         uint64                    `json:"tick"`
	Collectibles []CollectibleSnapshotItem `json:"collectibles"`
}

// CollectibleEnteredItem 新进入可见范围的收集品
type CollectibleEnteredItem struct {
	ID       uint64  `json:"id"`
	RegionID string  `json:"regionId"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
}

// CollectibleSpawnedItem 新生成的可见收集品
type CollectibleSpawnedItem struct {
	ID       uint64  `json:"id"`
	RegionID string  `json:"regionId"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
}

type SelfPosition struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type PlayerAppearanceUpdate struct {
	PlayerID   int64           `json:"playerId"`
	Appearance game.Appearance `json:"appearance"`
}

type ReplicationChanges struct {
	SelfPosition           *SelfPosition
	Entered                []game.PlayerState
	LeftPlayerIDs          []int64
	Positions              []game.PlayerPosition
	Appearances            []PlayerAppearanceUpdate
	CollectiblesEntered    []CollectibleEnteredItem
	CollectibleIDsLeft     []uint64
	CollectiblesSpawned    []CollectibleSpawnedItem
	CollectibleIDsCollected []uint64
}

type ReplicationUpdateMessage struct {
	Type                   string                   `json:"type"`
	Tick                   uint64                   `json:"tick"`
	SelfPosition           *SelfPosition            `json:"selfPosition,omitempty"`
	Entered                []game.PlayerState       `json:"entered,omitempty"`
	LeftPlayerIDs          []int64                  `json:"leftPlayerIds,omitempty"`
	Positions              []game.PlayerPosition    `json:"positions,omitempty"`
	Appearances            []PlayerAppearanceUpdate `json:"appearances,omitempty"`
	CollectiblesEntered    []CollectibleEnteredItem `json:"collectiblesEntered,omitempty"`
	CollectibleIDsLeft     []uint64                 `json:"collectibleIdsLeft,omitempty"`
	CollectiblesSpawned    []CollectibleSpawnedItem `json:"collectiblesSpawned,omitempty"`
	CollectibleIDsCollected []uint64                 `json:"collectibleIdsCollected,omitempty"`
}

func EncodeSelfState(tick uint64, player game.PlayerState, score int64) ([]byte, error) {
	return json.Marshal(SelfStateMessage{
		Type:   MessageTypeSelfState,
		Tick:   tick,
		Player: player,
		Score:  score,
	})
}

func EncodeVisibleEntitiesSnapshot(tick uint64, players []game.PlayerState) ([]byte, error) {
	var sorted []game.PlayerState
	if players == nil {
		sorted = []game.PlayerState{}
	} else {
		sorted = append([]game.PlayerState(nil), players...)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].ID < sorted[j].ID
		})
	}
	return json.Marshal(VisibleEntitiesSnapshotMessage{
		Type:    MessageTypeVisibleEntitiesSnapshot,
		Tick:    tick,
		Players: sorted,
	})
}

func EncodeCollectibleRegions(tick uint64, regions []game.CollectibleRegion) ([]byte, error) {
	public := make([]CollectibleRegionPublic, len(regions))
	for i, r := range regions {
		public[i] = CollectibleRegionPublic{
			ID:           r.ID,
			CenterLat:    r.CenterLat,
			CenterLng:    r.CenterLng,
			RadiusMeters: r.RadiusMeters,
		}
	}
	return json.Marshal(CollectibleRegionsMessage{
		Type:    MessageTypeCollectibleRegions,
		Tick:    tick,
		Regions: public,
	})
}

func EncodeVisibleCollectiblesSnapshot(tick uint64, collectibles []game.Collectible) ([]byte, error) {
	items := make([]CollectibleSnapshotItem, len(collectibles))
	for i, c := range collectibles {
		items[i] = CollectibleSnapshotItem{
			ID:       c.ID,
			RegionID: c.RegionID,
			Lat:      c.Lat,
			Lng:      c.Lng,
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return json.Marshal(VisibleCollectiblesSnapshotMessage{
		Type:         MessageTypeVisibleCollectiblesSnapshot,
		Tick:         tick,
		Collectibles: items,
	})
}

func EncodeCollectResult(collectibleID uint64, score int64) ([]byte, error) {
	return json.Marshal(CollectResultMessage{
		Type:          MessageTypeCollectResult,
		CollectibleID: collectibleID,
		Score:         score,
	})
}

func (c ReplicationChanges) IsEmpty() bool {
	return c.SelfPosition == nil &&
		len(c.Entered) == 0 &&
		len(c.LeftPlayerIDs) == 0 &&
		len(c.Positions) == 0 &&
		len(c.Appearances) == 0 &&
		len(c.CollectiblesEntered) == 0 &&
		len(c.CollectibleIDsLeft) == 0 &&
		len(c.CollectiblesSpawned) == 0 &&
		len(c.CollectibleIDsCollected) == 0
}

func NormalizeReplicationChanges(selfPlayerID int64, changes ReplicationChanges) ReplicationChanges {
	left := int64Set(changes.LeftPlayerIDs)
	entered := make([]game.PlayerState, 0, len(changes.Entered))
	enteredIDs := map[int64]struct{}{}

	for _, player := range changes.Entered {
		if player.ID == selfPlayerID || setContains(left, player.ID) {
			continue
		}
		entered = append(entered, player)
		enteredIDs[player.ID] = struct{}{}
	}
	sort.Slice(entered, func(i, j int) bool {
		return entered[i].ID < entered[j].ID
	})

	positions := make([]game.PlayerPosition, 0, len(changes.Positions))
	for _, position := range changes.Positions {
		if position.ID == selfPlayerID || setContains(left, position.ID) || setContains(enteredIDs, position.ID) {
			continue
		}
		positions = append(positions, position)
	}
	sort.Slice(positions, func(i, j int) bool {
		return positions[i].ID < positions[j].ID
	})

	appearances := normalizeAppearanceUpdates(changes.Appearances, left, enteredIDs)

	leftIDs := setKeys(left)
	sort.Slice(leftIDs, func(i, j int) bool { return leftIDs[i] < leftIDs[j] })
	filteredLeft := make([]int64, 0, len(leftIDs))
	for _, id := range leftIDs {
		if id != selfPlayerID {
			filteredLeft = append(filteredLeft, id)
		}
	}

	// 收集品归一化：同一 collectible 不能同时出现在矛盾集合中
	enteredColIDs := uint64SetFromEntered(changes.CollectiblesEntered)
	spawnedColIDs := uint64SetFromSpawned(changes.CollectiblesSpawned)
	leftColIDs := uint64Set(changes.CollectibleIDsLeft)
	collectedColIDs := uint64Set(changes.CollectibleIDsCollected)

	// 冲突消解: left > entered, collected > spawned
	// entered 与 collected 冲突 → collected 胜；spawned 与 left 冲突 → left 胜
	filterCollectibleIDs(enteredColIDs, leftColIDs)
	filterCollectibleIDs(enteredColIDs, collectedColIDs)
	filterCollectibleIDs(spawnedColIDs, leftColIDs)
	filterCollectibleIDs(spawnedColIDs, collectedColIDs)

	collectiblesEntered := filterCollectibleEntered(changes.CollectiblesEntered, spawnedColIDs, leftColIDs, collectedColIDs)
	collectiblesSpawned := filterCollectibleSpawned(changes.CollectiblesSpawned, leftColIDs, collectedColIDs)
	collectibleIDsLeft := sortedUint64Set(leftColIDs)
	collectibleIDsCollected := sortedUint64Set(collectedColIDs)

	return ReplicationChanges{
		SelfPosition:            changes.SelfPosition,
		Entered:                 entered,
		LeftPlayerIDs:           filteredLeft,
		Positions:               positions,
		Appearances:             appearances,
		CollectiblesEntered:     collectiblesEntered,
		CollectibleIDsLeft:      omitEmptyUint64s(collectibleIDsLeft),
		CollectiblesSpawned:     collectiblesSpawned,
		CollectibleIDsCollected: omitEmptyUint64s(collectibleIDsCollected),
	}
}

func EncodeReplicationUpdate(tick uint64, selfPlayerID int64, changes ReplicationChanges) ([]byte, error) {
	normalized := NormalizeReplicationChanges(selfPlayerID, changes)
	if normalized.IsEmpty() {
		return nil, ErrEmptyReplicationUpdate
	}
	return json.Marshal(ReplicationUpdateMessage{
		Type:                   MessageTypeReplicationUpdate,
		Tick:                   tick,
		SelfPosition:           normalized.SelfPosition,
		Entered:                omitEmptyPlayerStates(normalized.Entered),
		LeftPlayerIDs:          omitEmptyInt64s(normalized.LeftPlayerIDs),
		Positions:              omitEmptyPositions(normalized.Positions),
		Appearances:            omitEmptyAppearances(normalized.Appearances),
		CollectiblesEntered:    omitEmptyCollectibleEntered(normalized.CollectiblesEntered),
		CollectibleIDsLeft:     normalized.CollectibleIDsLeft,
		CollectiblesSpawned:    omitEmptyCollectibleSpawned(normalized.CollectiblesSpawned),
		CollectibleIDsCollected: normalized.CollectibleIDsCollected,
	})
}

func TryEncodeReplicationUpdate(tick uint64, selfPlayerID int64, changes ReplicationChanges) ([]byte, bool, error) {
	data, err := EncodeReplicationUpdate(tick, selfPlayerID, changes)
	if err == ErrEmptyReplicationUpdate {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func int64Set(values []int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func uint64Set(values []uint64) map[uint64]struct{} {
	set := make(map[uint64]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func uint64SetFromEntered(items []CollectibleEnteredItem) map[uint64]struct{} {
	set := make(map[uint64]struct{}, len(items))
	for _, item := range items {
		set[item.ID] = struct{}{}
	}
	return set
}

func uint64SetFromSpawned(items []CollectibleSpawnedItem) map[uint64]struct{} {
	set := make(map[uint64]struct{}, len(items))
	for _, item := range items {
		set[item.ID] = struct{}{}
	}
	return set
}

// filterCollectibleIDs 从 target 中移除出现在 keep 中的 ID
func filterCollectibleIDs(target, keep map[uint64]struct{}) {
	for id := range target {
		if _, ok := keep[id]; ok {
			delete(target, id)
		}
	}
}

func filterCollectibleEntered(items []CollectibleEnteredItem, spawnedIDs, leftIDs, collectedIDs map[uint64]struct{}) []CollectibleEnteredItem {
	result := make([]CollectibleEnteredItem, 0, len(items))
	for _, item := range items {
		if _, ok := spawnedIDs[item.ID]; ok {
			continue
		}
		if _, ok := leftIDs[item.ID]; ok {
			continue
		}
		if _, ok := collectedIDs[item.ID]; ok {
			continue
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func filterCollectibleSpawned(items []CollectibleSpawnedItem, leftIDs, collectedIDs map[uint64]struct{}) []CollectibleSpawnedItem {
	result := make([]CollectibleSpawnedItem, 0, len(items))
	for _, item := range items {
		if _, ok := leftIDs[item.ID]; ok {
			continue
		}
		if _, ok := collectedIDs[item.ID]; ok {
			continue
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func sortedUint64Set(set map[uint64]struct{}) []uint64 {
	ids := make([]uint64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func normalizeAppearanceUpdates(updates []PlayerAppearanceUpdate, left, enteredIDs map[int64]struct{}) []PlayerAppearanceUpdate {
	appearances := make([]PlayerAppearanceUpdate, 0)
	appearanceByPlayer := map[int64]PlayerAppearanceUpdate{}
	for _, update := range updates {
		if setContains(left, update.PlayerID) || setContains(enteredIDs, update.PlayerID) {
			continue
		}
		appearanceByPlayer[update.PlayerID] = update
	}
	for _, playerID := range sortedKeys(appearanceByPlayer) {
		appearances = append(appearances, appearanceByPlayer[playerID])
	}
	return appearances
}

func setContains(set map[int64]struct{}, value int64) bool {
	_, exists := set[value]
	return exists
}

func setKeys(set map[int64]struct{}) []int64 {
	keys := make([]int64, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}

func sortedKeys[V any](values map[int64]V) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func omitEmptyPlayerStates(values []game.PlayerState) []game.PlayerState {
	if len(values) == 0 {
		return nil
	}
	return values
}

func omitEmptyInt64s(values []int64) []int64 {
	if len(values) == 0 {
		return nil
	}
	return values
}

func omitEmptyUint64s(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	return values
}

func omitEmptyPositions(values []game.PlayerPosition) []game.PlayerPosition {
	if len(values) == 0 {
		return nil
	}
	return values
}

func omitEmptyAppearances(values []PlayerAppearanceUpdate) []PlayerAppearanceUpdate {
	if len(values) == 0 {
		return nil
	}
	return values
}

func omitEmptyCollectibleEntered(values []CollectibleEnteredItem) []CollectibleEnteredItem {
	if len(values) == 0 {
		return nil
	}
	return values
}

func omitEmptyCollectibleSpawned(values []CollectibleSpawnedItem) []CollectibleSpawnedItem {
	if len(values) == 0 {
		return nil
	}
	return values
}
