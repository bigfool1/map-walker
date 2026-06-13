package realtime

import (
	"encoding/json"
	"errors"
	"sort"

	"map-walker/internal/game"
)

var ErrEmptyReplicationUpdate = errors.New("empty replication update")

const (
	MessageTypeInput                   = "input"
	MessageTypeSelfState               = "self_state"
	MessageTypeVisibleEntitiesSnapshot = "visible_entities_snapshot"
	MessageTypeReplicationUpdate       = "replication_update"
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

type SelfStateMessage struct {
	Type   string           `json:"type"`
	Tick   uint64           `json:"tick"`
	Player game.PlayerState `json:"player"`
}

type VisibleEntitiesSnapshotMessage struct {
	Type    string             `json:"type"`
	Tick    uint64             `json:"tick"`
	Players []game.PlayerState `json:"players"`
}

type SelfPosition struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type PlayerAppearanceUpdate struct {
	PlayerID   string          `json:"playerId"`
	Appearance game.Appearance `json:"appearance"`
}

type ReplicationChanges struct {
	SelfPosition  *SelfPosition
	Entered       []game.PlayerState
	LeftPlayerIDs []string
	Positions     []game.PlayerPosition
	Appearances   []PlayerAppearanceUpdate
}

type ReplicationUpdateMessage struct {
	Type          string                   `json:"type"`
	Tick          uint64                   `json:"tick"`
	SelfPosition  *SelfPosition            `json:"selfPosition,omitempty"`
	Entered       []game.PlayerState       `json:"entered,omitempty"`
	LeftPlayerIDs []string                 `json:"leftPlayerIds,omitempty"`
	Positions     []game.PlayerPosition    `json:"positions,omitempty"`
	Appearances   []PlayerAppearanceUpdate `json:"appearances,omitempty"`
}

func EncodeSelfState(tick uint64, player game.PlayerState) ([]byte, error) {
	return json.Marshal(SelfStateMessage{
		Type:   MessageTypeSelfState,
		Tick:   tick,
		Player: player,
	})
}

func EncodeVisibleEntitiesSnapshot(tick uint64, players []game.PlayerState) ([]byte, error) {
	if players == nil {
		players = []game.PlayerState{}
	}
	return json.Marshal(VisibleEntitiesSnapshotMessage{
		Type:    MessageTypeVisibleEntitiesSnapshot,
		Tick:    tick,
		Players: players,
	})
}

func (c ReplicationChanges) IsEmpty() bool {
	return c.SelfPosition == nil &&
		len(c.Entered) == 0 &&
		len(c.LeftPlayerIDs) == 0 &&
		len(c.Positions) == 0 &&
		len(c.Appearances) == 0
}

func NormalizeReplicationChanges(selfPlayerID string, changes ReplicationChanges) ReplicationChanges {
	left := stringSet(changes.LeftPlayerIDs)
	entered := make([]game.PlayerState, 0, len(changes.Entered))
	enteredIDs := map[string]struct{}{}

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

	appearances := make([]PlayerAppearanceUpdate, 0)
	appearanceByPlayer := map[string]PlayerAppearanceUpdate{}
	for _, update := range changes.Appearances {
		if setContains(left, update.PlayerID) || setContains(enteredIDs, update.PlayerID) {
			continue
		}
		appearanceByPlayer[update.PlayerID] = update
	}
	for _, playerID := range sortedKeys(appearanceByPlayer) {
		appearances = append(appearances, appearanceByPlayer[playerID])
	}

	leftIDs := setKeys(left)
	sort.Strings(leftIDs)
	filteredLeft := make([]string, 0, len(leftIDs))
	for _, id := range leftIDs {
		if id != selfPlayerID {
			filteredLeft = append(filteredLeft, id)
		}
	}

	return ReplicationChanges{
		SelfPosition:  changes.SelfPosition,
		Entered:       entered,
		LeftPlayerIDs: filteredLeft,
		Positions:     positions,
		Appearances:   appearances,
	}
}

func EncodeReplicationUpdate(tick uint64, selfPlayerID string, changes ReplicationChanges) ([]byte, error) {
	normalized := NormalizeReplicationChanges(selfPlayerID, changes)
	if normalized.IsEmpty() {
		return nil, ErrEmptyReplicationUpdate
	}
	return json.Marshal(ReplicationUpdateMessage{
		Type:          MessageTypeReplicationUpdate,
		Tick:          tick,
		SelfPosition:  normalized.SelfPosition,
		Entered:       omitEmptyPlayerStates(normalized.Entered),
		LeftPlayerIDs: omitEmptyStrings(normalized.LeftPlayerIDs),
		Positions:     omitEmptyPositions(normalized.Positions),
		Appearances:   omitEmptyAppearances(normalized.Appearances),
	})
}

func TryEncodeReplicationUpdate(tick uint64, selfPlayerID string, changes ReplicationChanges) ([]byte, bool, error) {
	data, err := EncodeReplicationUpdate(tick, selfPlayerID, changes)
	if err == ErrEmptyReplicationUpdate {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func setContains(set map[string]struct{}, value string) bool {
	_, exists := set[value]
	return exists
}

func setKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func omitEmptyPlayerStates(values []game.PlayerState) []game.PlayerState {
	if len(values) == 0 {
		return nil
	}
	return values
}

func omitEmptyStrings(values []string) []string {
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
