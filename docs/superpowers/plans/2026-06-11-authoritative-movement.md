# Authoritative Movement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace client-supplied coordinates with a 20 Hz server-authoritative simulation and 10 Hz batched incremental state broadcasts.

**Architecture:** A pure `game.World` owns players, input state, simulation ticks, movement, and dirty/removal tracking. The existing Hub remains the single actor loop, adds simulation/broadcast/stat tickers, and is the only goroutine allowed to mutate World. Clients send normalized input state; new connections receive a full snapshot and existing connections receive only accumulated deltas.

**Tech Stack:** Go 1.26, `net/http`, `github.com/coder/websocket`, Go tests, plain JavaScript, Leaflet/Amap.

---

## File Migration

- Delete: `internal/game/state.go`
- Delete: `internal/game/state_test.go`
- Create: `internal/game/world.go`
  - Authoritative players, input sequencing, fixed-step movement, snapshots, and deltas.
- Create: `internal/game/world_test.go`
  - Deterministic tests with explicit durations; no goroutines or real timers.
- Replace: `internal/realtime/messages.go`
  - Remove `position_update` and `players_snapshot`; add input, world snapshot, and player delta messages.
- Replace: `internal/realtime/messages_test.go`
  - Test new protocol and confirm extra coordinate fields do not become authoritative state.
- Replace: `internal/realtime/hub.go`
  - Own World and connection map; process input and three ticker channels.
- Replace: `internal/realtime/hub_test.go`
  - Drive Hub with manual tick channels for deterministic tests.
- Modify: `internal/realtime/client.go`
  - Decode input state and submit it with client identity.
- Replace: `web/app.js`
  - Send state transitions instead of coordinates; render snapshots and deltas.
- Modify: `README.md`
  - Document authoritative movement and new protocol.
- Modify: `AGENTS.md`
  - Update package responsibilities and message names.

`cmd/map-walker/main.go`, `internal/server/server.go`, HTML, CSS, Leaflet assets,
and map tile configuration remain unchanged unless manual verification exposes a
specific bug.

---

## Task 1: Add Authoritative World

**Files:**
- Create: `internal/game/world.go`
- Create: `internal/game/world_test.go`
- Modify: `internal/game/state.go`

- [ ] **Step 1: Write World tests**

Create `internal/game/world_test.go`:

```go
package game

import (
	"math"
	"testing"
	"time"
)

func TestWorldAddPlayerUsesConfiguredSpawn(t *testing.T) {
	world := NewWorld(Config{
		SpawnLat:            31.2304,
		SpawnLng:            121.4737,
		SpeedMetersPerSecond: 12,
	})

	if added := world.AddPlayer("alice"); !added {
		t.Fatal("expected alice to be added")
	}

	snapshot := world.Snapshot()
	if snapshot.Tick != 0 {
		t.Fatalf("expected tick 0, got %d", snapshot.Tick)
	}
	if len(snapshot.Players) != 1 {
		t.Fatalf("expected 1 player, got %d", len(snapshot.Players))
	}
	if snapshot.Players[0] != (PlayerPosition{ID: "alice", Lat: 31.2304, Lng: 121.4737}) {
		t.Fatalf("unexpected spawn: %+v", snapshot.Players[0])
	}
}

func TestWorldApplyInputRejectsOldSequence(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")

	if accepted := world.ApplyInput("alice", InputState{Sequence: 2, Right: true}); !accepted {
		t.Fatal("expected newer input to be accepted")
	}
	if accepted := world.ApplyInput("alice", InputState{Sequence: 2, Left: true}); accepted {
		t.Fatal("expected duplicate sequence to be rejected")
	}
	if accepted := world.ApplyInput("alice", InputState{Sequence: 1, Left: true}); accepted {
		t.Fatal("expected stale sequence to be rejected")
	}

	world.Step(time.Second)
	position := world.Snapshot().Players[0]
	if position.Lng <= 121.4737 {
		t.Fatalf("expected accepted right input to win, got %+v", position)
	}
}

func TestWorldMovementDependsOnDurationNotInputFrequency(t *testing.T) {
	oneMessage := newTestWorld()
	oneMessage.AddPlayer("alice")
	oneMessage.ApplyInput("alice", InputState{Sequence: 1, Right: true})
	for range 20 {
		oneMessage.Step(50 * time.Millisecond)
	}

	manyMessages := newTestWorld()
	manyMessages.AddPlayer("alice")
	for sequence := uint64(1); sequence <= 20; sequence++ {
		manyMessages.ApplyInput("alice", InputState{Sequence: sequence, Right: true})
		manyMessages.Step(50 * time.Millisecond)
	}

	one := oneMessage.Snapshot().Players[0]
	many := manyMessages.Snapshot().Players[0]
	if !almostEqual(one.Lat, many.Lat) || !almostEqual(one.Lng, many.Lng) {
		t.Fatalf("input frequency changed movement: one=%+v many=%+v", one, many)
	}
}

func TestWorldDiagonalSpeedMatchesStraightSpeed(t *testing.T) {
	straight := newTestWorld()
	straight.AddPlayer("alice")
	straight.ApplyInput("alice", InputState{Sequence: 1, Right: true})
	straight.Step(time.Second)

	diagonal := newTestWorld()
	diagonal.AddPlayer("alice")
	diagonal.ApplyInput("alice", InputState{Sequence: 1, Up: true, Right: true})
	diagonal.Step(time.Second)

	straightDistance := distanceMeters(testConfig().SpawnLat, testConfig().SpawnLng, straight.Snapshot().Players[0])
	diagonalDistance := distanceMeters(testConfig().SpawnLat, testConfig().SpawnLng, diagonal.Snapshot().Players[0])
	if math.Abs(straightDistance-diagonalDistance) > 0.01 {
		t.Fatalf("expected equal speeds: straight=%f diagonal=%f", straightDistance, diagonalDistance)
	}
}

func TestWorldOppositeDirectionsCancel(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")
	world.TakeDelta()
	world.ApplyInput("alice", InputState{
		Sequence: 1,
		Up:       true,
		Down:     true,
		Left:     true,
		Right:    true,
	})

	world.Step(time.Second)

	if delta := world.TakeDelta(); delta.HasChanges() {
		t.Fatalf("expected no movement delta, got %+v", delta)
	}
}

func TestWorldMergesSeveralStepsIntoLatestDelta(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")
	world.TakeDelta()
	world.ApplyInput("alice", InputState{Sequence: 1, Up: true})

	world.Step(50 * time.Millisecond)
	world.Step(50 * time.Millisecond)

	delta := world.TakeDelta()
	if delta.Tick != 2 {
		t.Fatalf("expected tick 2, got %d", delta.Tick)
	}
	if len(delta.Players) != 1 || delta.Players[0].ID != "alice" {
		t.Fatalf("expected one latest alice position, got %+v", delta.Players)
	}
	if next := world.TakeDelta(); next.HasChanges() {
		t.Fatalf("expected TakeDelta to clear changes, got %+v", next)
	}
}

func TestWorldRemovePlayerReportsOnlyRemoval(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")
	world.RemovePlayer("alice")

	delta := world.TakeDelta()
	if len(delta.Players) != 0 {
		t.Fatalf("removed player must not remain dirty: %+v", delta.Players)
	}
	if len(delta.RemovedPlayerIDs) != 1 || delta.RemovedPlayerIDs[0] != "alice" {
		t.Fatalf("unexpected removals: %+v", delta.RemovedPlayerIDs)
	}
}

func TestWorldResetInputAllowsReplacementSequenceToRestart(t *testing.T) {
	world := newTestWorld()
	world.AddPlayer("alice")
	world.ApplyInput("alice", InputState{Sequence: 100, Right: true})

	world.ResetInput("alice")

	if accepted := world.ApplyInput("alice", InputState{Sequence: 1, Left: true}); !accepted {
		t.Fatal("expected replacement connection sequence to restart")
	}
}

func newTestWorld() *World {
	return NewWorld(testConfig())
}

func testConfig() Config {
	return Config{
		SpawnLat:            31.2304,
		SpawnLng:            121.4737,
		SpeedMetersPerSecond: 12,
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}

func distanceMeters(startLat, startLng float64, end PlayerPosition) float64 {
	latMeters := (end.Lat - startLat) * metersPerDegreeLatitude
	lngMeters := (end.Lng - startLng) * metersPerDegreeLongitude(startLat)
	return math.Hypot(latMeters, lngMeters)
}
```

- [ ] **Step 2: Run the World tests and verify failure**

Run:

```bash
go test ./internal/game
```

Expected: FAIL because `World`, `Config`, `InputState`, and related helpers are
undefined.

- [ ] **Step 3: Implement World**

Create `internal/game/world.go`:

```go
package game

import (
	"math"
	"sort"
	"time"
)

const metersPerDegreeLatitude = 111_320.0

type Config struct {
	SpawnLat             float64
	SpawnLng             float64
	SpeedMetersPerSecond float64
}

func DefaultConfig() Config {
	return Config{
		SpawnLat:             31.2304,
		SpawnLng:             121.4737,
		SpeedMetersPerSecond: 12,
	}
}

type InputState struct {
	Sequence uint64
	Up       bool
	Down     bool
	Left     bool
	Right    bool
}

type PlayerPosition struct {
	ID  string  `json:"id"`
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type Snapshot struct {
	Tick    uint64
	Players []PlayerPosition
}

type Delta struct {
	Tick             uint64
	Players          []PlayerPosition
	RemovedPlayerIDs []string
}

func (d Delta) HasChanges() bool {
	return len(d.Players) > 0 || len(d.RemovedPlayerIDs) > 0
}

type player struct {
	position     PlayerPosition
	input        InputState
	lastSequence uint64
}

type World struct {
	config           Config
	players          map[string]*player
	tick             uint64
	dirtyPlayerIDs   map[string]struct{}
	removedPlayerIDs map[string]struct{}
}

func NewWorld(config Config) *World {
	return &World{
		config:           config,
		players:          map[string]*player{},
		dirtyPlayerIDs:   map[string]struct{}{},
		removedPlayerIDs: map[string]struct{}{},
	}
}

func (w *World) AddPlayer(playerID string) bool {
	if _, exists := w.players[playerID]; exists {
		return false
	}

	w.players[playerID] = &player{
		position: PlayerPosition{
			ID:  playerID,
			Lat: w.config.SpawnLat,
			Lng: w.config.SpawnLng,
		},
	}
	w.dirtyPlayerIDs[playerID] = struct{}{}
	delete(w.removedPlayerIDs, playerID)
	return true
}

func (w *World) RemovePlayer(playerID string) bool {
	if _, exists := w.players[playerID]; !exists {
		return false
	}

	delete(w.players, playerID)
	delete(w.dirtyPlayerIDs, playerID)
	w.removedPlayerIDs[playerID] = struct{}{}
	return true
}

func (w *World) ResetInput(playerID string) {
	p, exists := w.players[playerID]
	if !exists {
		return
	}
	p.input = InputState{}
	p.lastSequence = 0
}

func (w *World) ApplyInput(playerID string, input InputState) bool {
	p, exists := w.players[playerID]
	if !exists || input.Sequence <= p.lastSequence {
		return false
	}

	p.input = input
	p.lastSequence = input.Sequence
	return true
}

func (w *World) Step(deltaTime time.Duration) {
	w.tick++
	distance := w.config.SpeedMetersPerSecond * deltaTime.Seconds()

	for playerID, p := range w.players {
		x := boolNumber(p.input.Right) - boolNumber(p.input.Left)
		y := boolNumber(p.input.Up) - boolNumber(p.input.Down)
		if x == 0 && y == 0 {
			continue
		}

		length := math.Hypot(x, y)
		x /= length
		y /= length

		p.position.Lat += y * distance / metersPerDegreeLatitude
		p.position.Lng += x * distance / metersPerDegreeLongitude(p.position.Lat)
		w.dirtyPlayerIDs[playerID] = struct{}{}
	}
}

func (w *World) Snapshot() Snapshot {
	return Snapshot{
		Tick:    w.tick,
		Players: w.positionsFor(w.playersKeys()),
	}
}

func (w *World) TakeDelta() Delta {
	playerIDs := setKeys(w.dirtyPlayerIDs)
	removedIDs := setKeys(w.removedPlayerIDs)

	delta := Delta{
		Tick:             w.tick,
		Players:          w.positionsFor(playerIDs),
		RemovedPlayerIDs: removedIDs,
	}

	clear(w.dirtyPlayerIDs)
	clear(w.removedPlayerIDs)
	return delta
}

func (w *World) PlayerCount() int {
	return len(w.players)
}

func (w *World) playersKeys() []string {
	ids := make([]string, 0, len(w.players))
	for id := range w.players {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (w *World) positionsFor(ids []string) []PlayerPosition {
	positions := make([]PlayerPosition, 0, len(ids))
	for _, id := range ids {
		if p, exists := w.players[id]; exists {
			positions = append(positions, p.position)
		}
	}
	return positions
}

func setKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func boolNumber(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func metersPerDegreeLongitude(latitude float64) float64 {
	return metersPerDegreeLatitude * math.Cos(latitude*math.Pi/180)
}
```

- [ ] **Step 4: Move `PlayerPosition` ownership out of phase-one State**

Delete this declaration from `internal/game/state.go`:

```go
type PlayerPosition struct {
	ID  string  `json:"id"`
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}
```

Do not otherwise change `State`. It temporarily reuses the `PlayerPosition`
declared in `world.go`, keeping phase-one realtime code buildable.

- [ ] **Step 5: Run and format World tests**

Run:

```bash
gofmt -w internal/game/world.go internal/game/world_test.go
go test ./internal/game
go test ./...
```

Expected: both commands PASS. Keep phase-one `state.go` and `state_test.go`
temporarily so the existing realtime package continues to compile.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/game
git commit -m "feat: add authoritative game world"
```

---

## Task 2: Add The New WebSocket Protocol Alongside Phase One

**Files:**
- Modify: `internal/realtime/messages.go`
- Replace: `internal/realtime/messages_test.go`

- [ ] **Step 1: Replace protocol tests**

Replace `internal/realtime/messages_test.go` with:

```go
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
		Players: []game.PlayerPosition{
			{ID: "alice", Lat: 31.2304, Lng: 121.4737},
		},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"world_snapshot","tick":7,"players":[{"id":"alice","lat":31.2304,"lng":121.4737}]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}

func TestEncodePlayersDelta(t *testing.T) {
	data, err := EncodePlayersDelta(game.Delta{
		Tick: 9,
		Players: []game.PlayerPosition{
			{ID: "alice", Lat: 31.2305, Lng: 121.4738},
		},
		RemovedPlayerIDs: []string{"bob"},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	want := `{"type":"players_delta","tick":9,"players":[{"id":"alice","lat":31.2305,"lng":121.4738}],"removedPlayerIds":["bob"]}`
	if string(data) != want {
		t.Fatalf("unexpected json:\nwant %s\n got %s", want, string(data))
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/realtime
```

Expected: FAIL because the new message names and encoding functions do not
exist.

- [ ] **Step 3: Add new message types while retaining old types temporarily**

Replace `internal/realtime/messages.go` with:

```go
package realtime

import (
	"encoding/json"

	"map-walker/internal/game"
)

const (
	MessageTypeInput         = "input"
	MessageTypeWorldSnapshot = "world_snapshot"
	MessageTypePlayersDelta  = "players_delta"

	// Phase-one names remain only until Hub and Client migrate in Tasks 3-4.
	MessageTypePositionUpdate  = "position_update"
	MessageTypePlayersSnapshot = "players_snapshot"
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

func NewPlayersSnapshotMessage(players []game.PlayerPosition) PlayersSnapshotMessage {
	return PlayersSnapshotMessage{
		Type:    MessageTypePlayersSnapshot,
		Players: players,
	}
}
```

- [ ] **Step 4: Format and run only protocol tests**

Run:

```bash
gofmt -w internal/realtime/messages.go internal/realtime/messages_test.go
go test ./internal/realtime -run 'Test(DecodeInputMessage|EncodeWorldSnapshot|EncodePlayersDelta)'
```

Expected: PASS. The temporary phase-one definitions keep existing Hub and Client
compiling while the new protocol is introduced.

- [ ] **Step 5: Run all tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/realtime/messages.go internal/realtime/messages_test.go
git commit -m "feat: add authoritative movement protocol"
```

---

## Task 3: Replace Hub With Tick-Driven World Ownership

**Files:**
- Replace: `internal/realtime/hub.go`
- Replace: `internal/realtime/hub_test.go`
- Delete: `internal/game/state.go`
- Delete: `internal/game/state_test.go`

- [ ] **Step 1: Replace Hub tests**

Replace `internal/realtime/hub_test.go` with:

```go
package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"map-walker/internal/game"
)

func TestHubRegisterSendsSnapshotAndDefersDelta(t *testing.T) {
	hub, _, broadcasts := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	if !hub.Register(alice) {
		t.Fatal("register failed")
	}
	snapshot := mustReceiveSnapshot(t, alice)
	if len(snapshot.Players) != 1 || snapshot.Players[0].ID != "alice" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	delta := mustReceiveDelta(t, alice)
	if len(delta.Players) != 1 || delta.Players[0].ID != "alice" {
		t.Fatalf("expected deferred alice delta, got %+v", delta)
	}
}

func TestHubSimulationDoesNotBroadcastUntilBroadcastTick(t *testing.T) {
	hub, simulations, broadcasts := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	delta := mustReceiveDelta(t, alice)
	if delta.Tick != 1 || len(delta.Players) != 1 {
		t.Fatalf("unexpected movement delta: %+v", delta)
	}
}

func TestHubEmptyBroadcastTickSendsNothing(t *testing.T) {
	hub, _, broadcasts := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
}

func TestHubDisconnectAppearsInNextDelta(t *testing.T) {
	hub, _, broadcasts := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 8)
	bob := NewTestClient("bob", 8)
	hub.Register(alice)
	mustReceiveSnapshot(t, alice)
	hub.Register(bob)
	mustReceiveSnapshot(t, bob)
	broadcasts <- time.Now()
	mustReceiveDelta(t, alice)
	mustReceiveDelta(t, bob)

	hub.Unregister(bob)
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	delta := mustReceiveDelta(t, alice)
	if len(delta.RemovedPlayerIDs) != 1 || delta.RemovedPlayerIDs[0] != "bob" {
		t.Fatalf("unexpected removals: %+v", delta.RemovedPlayerIDs)
	}
}

func TestHubRejectsInputFromReplacedConnection(t *testing.T) {
	hub, simulations, broadcasts := newTestHub()
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient("alice", 8)
	replacement := NewTestClient("alice", 8)
	hub.Register(old)
	mustReceiveSnapshot(t, old)
	broadcasts <- time.Now()
	mustReceiveDelta(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	hub.Register(replacement)
	mustReceiveSnapshot(t, replacement)
	hub.ApplyInput(old, game.InputState{Sequence: 2, Right: true})
	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	delta := mustReceiveDelta(t, replacement)
	if len(delta.Players) != 1 {
		t.Fatalf("unexpected replacement delta: %+v", delta)
	}
	if delta.Players[0].Lng >= testWorldConfig().SpawnLng {
		t.Fatalf("stale old connection controlled player: %+v", delta.Players[0])
	}
	if len(delta.RemovedPlayerIDs) != 0 {
		t.Fatalf("replacement must not emit removal: %+v", delta.RemovedPlayerIDs)
	}

	hub.Unregister(old)
	broadcasts <- time.Now()
	assertNoMessage(t, replacement)
}

func TestHubDropsSlowClient(t *testing.T) {
	hub, _, broadcasts := newTestHub()
	go hub.Run()
	defer hub.Stop()

	slow := NewTestClient("slow", 0)
	fast := NewTestClient("fast", 8)
	hub.Register(slow)
	hub.Register(fast)
	mustReceiveSnapshot(t, fast)
	broadcasts <- time.Now()
	mustReceiveDelta(t, fast)

	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("expected slow client to close")
	}
}

func TestHubMethodsReturnAfterStop(t *testing.T) {
	hub, _, _ := newTestHub()
	go hub.Run()
	hub.Stop()

	client := NewTestClient("alice", 1)
	if hub.Register(client) {
		t.Fatal("register should fail after stop")
	}
	if hub.ApplyInput(client, game.InputState{Sequence: 1, Up: true}) {
		t.Fatal("input should fail after stop")
	}
	hub.Unregister(client)
}

func newTestHub() (*Hub, chan time.Time, chan time.Time) {
	simulations := make(chan time.Time, 8)
	broadcasts := make(chan time.Time, 8)
	world := game.NewWorld(testWorldConfig())
	hub := newHub(world, simulations, broadcasts, nil, func() {})
	return hub, simulations, broadcasts
}

func testWorldConfig() game.Config {
	return game.Config{
		SpawnLat:             31.2304,
		SpawnLng:             121.4737,
		SpeedMetersPerSecond: 12,
	}
}

type testClient struct {
	id   string
	send chan []byte
	done chan struct{}
}

func NewTestClient(id string, buffer int) *testClient {
	return &testClient{
		id:   id,
		send: make(chan []byte, buffer),
		done: make(chan struct{}),
	}
}

func (c *testClient) ID() string {
	return c.id
}

func (c *testClient) Send(data []byte) bool {
	select {
	case c.send <- data:
		return true
	default:
		return false
	}
}

func (c *testClient) CloseSend() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

func mustReceiveSnapshot(t *testing.T, client *testClient) WorldSnapshotMessage {
	t.Helper()
	data := mustReceiveData(t, client)
	var message WorldSnapshotMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode snapshot failed: %v", err)
	}
	if message.Type != MessageTypeWorldSnapshot {
		t.Fatalf("expected snapshot, got %q", message.Type)
	}
	return message
}

func mustReceiveDelta(t *testing.T, client *testClient) PlayersDeltaMessage {
	t.Helper()
	data := mustReceiveData(t, client)
	var message PlayersDeltaMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode delta failed: %v", err)
	}
	if message.Type != MessageTypePlayersDelta {
		t.Fatalf("expected delta, got %q", message.Type)
	}
	return message
}

func mustReceiveData(t *testing.T, client *testClient) []byte {
	t.Helper()
	select {
	case data := <-client.send:
		return data
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

func assertNoMessage(t *testing.T, client *testClient) {
	t.Helper()
	select {
	case data := <-client.send:
		t.Fatalf("unexpected message: %s", data)
	case <-time.After(20 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Replace Hub implementation**

Replace `internal/realtime/hub.go` with:

```go
package realtime

import (
	"log"
	"sync"
	"time"

	"map-walker/internal/game"
)

const (
	simulationInterval = 50 * time.Millisecond
	broadcastInterval  = 100 * time.Millisecond
	statsInterval      = time.Second
)

type ClientSender interface {
	ID() string
	Send([]byte) bool
	CloseSend()
}

type inputEvent struct {
	client ClientSender
	input  game.InputState
}

type Hub struct {
	world          *game.World
	register       chan ClientSender
	unregister     chan ClientSender
	inputs         chan inputEvent
	stop           chan struct{}
	done           chan struct{}
	stopOnce       sync.Once
	clients        map[string]ClientSender
	simulationTick <-chan time.Time
	broadcastTick  <-chan time.Time
	statsTick      <-chan time.Time
	stopTickers    func()
	stats          intervalStats
}

type intervalStats struct {
	acceptedInputs  uint64
	simulationTicks uint64
	deltaBroadcasts uint64
	changedPlayers  uint64
	deltaBytes      uint64
}

func NewHub() *Hub {
	simulationTicker := time.NewTicker(simulationInterval)
	broadcastTicker := time.NewTicker(broadcastInterval)
	statsTicker := time.NewTicker(statsInterval)

	return newHub(
		game.NewWorld(game.DefaultConfig()),
		simulationTicker.C,
		broadcastTicker.C,
		statsTicker.C,
		func() {
			simulationTicker.Stop()
			broadcastTicker.Stop()
			statsTicker.Stop()
		},
	)
}

func newHub(
	world *game.World,
	simulationTick <-chan time.Time,
	broadcastTick <-chan time.Time,
	statsTick <-chan time.Time,
	stopTickers func(),
) *Hub {
	return &Hub{
		world:          world,
		register:       make(chan ClientSender),
		unregister:     make(chan ClientSender),
		inputs:         make(chan inputEvent),
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
		clients:        map[string]ClientSender{},
		simulationTick: simulationTick,
		broadcastTick:  broadcastTick,
		statsTick:      statsTick,
		stopTickers:    stopTickers,
	}
}

// Run is the single owner of both connections and authoritative world state.
//
// Python comparison: this is one long-running asyncio task selecting between
// queue events and timer events. World itself stays synchronous and
// deterministic; only this orchestration layer knows about concurrency.
func (h *Hub) Run() {
	defer close(h.done)
	defer h.stopTickers()

	for {
		select {
		case client := <-h.register:
			h.registerClient(client)
		case client := <-h.unregister:
			h.removeClient(client)
		case event := <-h.inputs:
			if h.clients[event.client.ID()] == event.client &&
				h.world.ApplyInput(event.client.ID(), event.input) {
				h.stats.acceptedInputs++
			}
		case <-h.simulationTick:
			h.world.Step(simulationInterval)
			h.stats.simulationTicks++
		case <-h.broadcastTick:
			h.broadcastDelta()
		case <-h.statsTick:
			h.logStats()
		case <-h.stop:
			for _, client := range h.clients {
				client.CloseSend()
			}
			return
		}
	}
}

func (h *Hub) Stop() {
	h.stopOnce.Do(func() {
		close(h.stop)
	})
	<-h.done
}

func (h *Hub) Register(client ClientSender) bool {
	select {
	case h.register <- client:
		return true
	case <-h.done:
		return false
	}
}

func (h *Hub) Unregister(client ClientSender) {
	select {
	case h.unregister <- client:
	case <-h.done:
	}
}

func (h *Hub) ApplyInput(client ClientSender, input game.InputState) bool {
	select {
	case h.inputs <- inputEvent{client: client, input: input}:
		return true
	case <-h.done:
		return false
	}
}

func (h *Hub) registerClient(client ClientSender) {
	if existing, exists := h.clients[client.ID()]; exists && existing != client {
		existing.CloseSend()
		h.world.ResetInput(client.ID())
	} else {
		h.world.AddPlayer(client.ID())
	}

	h.clients[client.ID()] = client
	h.sendSnapshot(client)
}

func (h *Hub) removeClient(client ClientSender) {
	current, exists := h.clients[client.ID()]
	if !exists || current != client {
		return
	}

	delete(h.clients, client.ID())
	h.world.RemovePlayer(client.ID())
	client.CloseSend()
}

func (h *Hub) sendSnapshot(client ClientSender) {
	data, err := EncodeWorldSnapshot(h.world.Snapshot())
	if err != nil {
		log.Printf("encode world snapshot failed: %v", err)
		h.removeClient(client)
		return
	}
	if ok := client.Send(data); !ok {
		h.removeClient(client)
	}
}

func (h *Hub) broadcastDelta() {
	delta := h.world.TakeDelta()
	if !delta.HasChanges() {
		return
	}

	data, err := EncodePlayersDelta(delta)
	if err != nil {
		log.Printf("encode players delta failed: %v", err)
		return
	}

	h.stats.deltaBroadcasts++
	h.stats.changedPlayers += uint64(len(delta.Players))
	h.stats.deltaBytes += uint64(len(data))

	for _, client := range h.clients {
		if ok := client.Send(data); !ok {
			h.removeClient(client)
		}
	}
}

func (h *Hub) logStats() {
	log.Printf(
		"realtime stats clients=%d inputs=%d simulation_ticks=%d delta_broadcasts=%d changed_players=%d delta_bytes=%d",
		len(h.clients),
		h.stats.acceptedInputs,
		h.stats.simulationTicks,
		h.stats.deltaBroadcasts,
		h.stats.changedPlayers,
		h.stats.deltaBytes,
	)
	h.stats = intervalStats{}
}
```

- [ ] **Step 3: Delete phase-one State**

Delete `internal/game/state.go` and `internal/game/state_test.go` with
`apply_patch`. `PlayerPosition` now lives in `world.go`.

- [ ] **Step 4: Format and run realtime tests**

Run:

```bash
gofmt -w internal/realtime/messages.go internal/realtime/messages_test.go internal/realtime/hub.go internal/realtime/hub_test.go
go test ./internal/realtime
```

Expected: protocol and Hub tests PASS. `client.go` still compiles because Task 2
temporarily retained the phase-one message definitions.

- [ ] **Step 5: Run all tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit Hub migration**

Run:

```bash
git add -A internal/game internal/realtime/hub.go internal/realtime/hub_test.go
git commit -m "feat: add tick-driven authoritative hub"
```

---

## Task 4: Migrate Client Read Loop To Input Messages

**Files:**
- Modify: `internal/realtime/client.go`
- Modify: `internal/realtime/messages.go`

- [ ] **Step 1: Replace the read-loop decode block**

Replace `readLoop` with:

```go
func (c *Client) readLoop(ctx context.Context) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}

		var message InputMessage
		if err := json.Unmarshal(data, &message); err != nil {
			log.Printf("decode websocket message failed: %v", err)
			continue
		}
		if message.Type != MessageTypeInput {
			continue
		}

		if ok := c.hub.ApplyInput(c, message.InputState()); !ok {
			return
		}
	}
}
```

- [ ] **Step 2: Make registration failure end the client**

Replace the start of `Run` with:

```go
func (c *Client) Run(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	if ok := c.hub.Register(c); !ok {
		return
	}
	defer c.hub.Unregister(c)

	go c.writeLoop(ctx)
	c.readLoop(ctx)
}
```

- [ ] **Step 3: Remove phase-one message definitions**

From `internal/realtime/messages.go`, delete:

```go
MessageTypePositionUpdate  = "position_update"
MessageTypePlayersSnapshot = "players_snapshot"
```

and delete `PositionUpdateMessage`, `PlayersSnapshotMessage`, and
`NewPlayersSnapshotMessage`.

- [ ] **Step 4: Format and run all Go tests**

Run:

```bash
gofmt -w internal/realtime/client.go
go test ./...
go vet ./...
```

Expected: both commands PASS.

- [ ] **Step 5: Confirm old protocol is gone**

Run:

```bash
rg -n 'position_update|players_snapshot|PositionUpdateMessage|PlayersSnapshotMessage' internal
```

Expected: no matches.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/realtime/client.go internal/realtime/messages.go
git commit -m "feat: accept authoritative movement input"
```

---

## Task 5: Replace Frontend Movement With Input State

**Files:**
- Replace: `web/app.js`

- [ ] **Step 1: Replace frontend JavaScript**

Replace `web/app.js` with:

```javascript
const startPosition = { lat: 31.2304, lng: 121.4737 };
const playerId = getOrCreatePlayerId();
const markers = new Map();
const input = {
  up: false,
  down: false,
  left: false,
  right: false,
};

let inputSequence = 0;
let socket = null;

const map = L.map("map", { zoomControl: true }).setView(
  [startPosition.lat, startPosition.lng],
  16
);

L.tileLayer(
  "https://webrd0{s}.is.autonavi.com/appmaptile?lang=zh_cn&size=1&scale=1&style=8&x={x}&y={y}&z={z}",
  {
    maxZoom: 18,
    subdomains: "1234",
    attribution: "&copy; 高德地图",
  }
).addTo(map);

L.Icon.Default.imagePath = "/images/";

connect();
bindKeyboardControls();
bindDpadControls();
bindInputSafetyControls();

function getOrCreatePlayerId() {
  const key = "map-walker-player-id";
  const existing = sessionStorage.getItem(key);
  if (existing) {
    return existing;
  }
  const created = `p-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
  sessionStorage.setItem(key, created);
  return created;
}

function connect() {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const url = `${protocol}//${window.location.host}/ws?playerId=${encodeURIComponent(playerId)}`;
  socket = new WebSocket(url);
  setStatus("connecting");

  socket.addEventListener("open", () => {
    setStatus("connected");
    sendInput();
  });

  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (message.type === "world_snapshot") {
      renderSnapshot(message.players);
    } else if (message.type === "players_delta") {
      renderDelta(message.players, message.removedPlayerIds);
    }
  });

  socket.addEventListener("close", () => {
    setStatus("disconnected");
  });

  socket.addEventListener("error", () => {
    setStatus("disconnected");
  });
}

function setDirection(direction, pressed) {
  if (input[direction] === pressed) {
    return;
  }
  input[direction] = pressed;
  sendInput();
}

function clearInput() {
  const changed = input.up || input.down || input.left || input.right;
  input.up = false;
  input.down = false;
  input.left = false;
  input.right = false;
  if (changed) {
    sendInput();
  }
}

function sendInput() {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }

  inputSequence += 1;
  socket.send(
    JSON.stringify({
      type: "input",
      sequence: inputSequence,
      up: input.up,
      down: input.down,
      left: input.left,
      right: input.right,
    })
  );
}

function renderSnapshot(players) {
  const liveIds = new Set(players.map((player) => player.id));
  for (const [id, marker] of markers.entries()) {
    if (!liveIds.has(id)) {
      marker.remove();
      markers.delete(id);
    }
  }
  updatePlayers(players);
}

function renderDelta(players, removedPlayerIds) {
  for (const playerIdToRemove of removedPlayerIds) {
    const marker = markers.get(playerIdToRemove);
    if (marker) {
      marker.remove();
      markers.delete(playerIdToRemove);
    }
  }
  updatePlayers(players);
}

function updatePlayers(players) {
  for (const player of players) {
    const marker = markers.get(player.id);
    const latLng = [player.lat, player.lng];
    if (marker) {
      marker.setLatLng(latLng);
    } else {
      const label = player.id === playerId ? "You" : "Player";
      markers.set(player.id, L.marker(latLng).addTo(map).bindTooltip(label));
    }

    if (player.id === playerId) {
      map.panTo(latLng, { animate: true });
    }
  }
}

function bindKeyboardControls() {
  const directions = {
    ArrowUp: "up",
    w: "up",
    ArrowDown: "down",
    s: "down",
    ArrowLeft: "left",
    a: "left",
    ArrowRight: "right",
    d: "right",
  };

  window.addEventListener("keydown", (event) => {
    const direction = directions[event.key] || directions[event.key.toLowerCase()];
    if (!direction) {
      return;
    }
    event.preventDefault();
    setDirection(direction, true);
  });

  window.addEventListener("keyup", (event) => {
    const direction = directions[event.key] || directions[event.key.toLowerCase()];
    if (!direction) {
      return;
    }
    event.preventDefault();
    setDirection(direction, false);
  });
}

function bindDpadControls() {
  // Keyboard and touch normalize into the same persistent input state. Unlike
  // phase one, the browser no longer runs a movement timer or computes
  // coordinates; the server decides movement on its fixed simulation ticks.
  for (const button of document.querySelectorAll("[data-move]")) {
    const direction = button.dataset.move;

    button.addEventListener("pointerdown", (event) => {
      event.preventDefault();
      button.setPointerCapture(event.pointerId);
      setDirection(direction, true);
    });
    button.addEventListener("pointerup", () => setDirection(direction, false));
    button.addEventListener("pointercancel", () => setDirection(direction, false));
    button.addEventListener("lostpointercapture", () => setDirection(direction, false));
  }
}

function bindInputSafetyControls() {
  window.addEventListener("blur", clearInput);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      clearInput();
    }
  });
}

function setStatus(status) {
  const element = document.getElementById("status");
  element.textContent = status.charAt(0).toUpperCase() + status.slice(1);
  element.className = `status status--${status}`;
}
```

- [ ] **Step 2: Confirm phase-one coordinate code is gone**

Run:

```bash
rg -n 'position_update|sendPosition|movePlayer|currentPosition|setInterval' web internal
```

Expected: no matches.

- [ ] **Step 3: Run Go verification**

Run:

```bash
go test ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git add web/app.js
git commit -m "feat: send movement intent from browser"
```

---

## Task 6: Update Project Documentation

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`

- [ ] **Step 1: Replace README architecture and protocol sections**

Keep Quick Start unchanged. Replace the descriptive introduction, Architecture,
and WebSocket Protocol sections so they include:

````markdown
# Map Walker

A small server-authoritative multiplayer movement demo built with Go and
Leaflet. Browsers send keyboard or touch input; the Go server owns player
positions, simulates movement at 20 Hz, and broadcasts changed players at 10 Hz.

## Architecture

```text
cmd/map-walker/      — entry point, wires Hub + HTTP server
internal/server/     — routes, static files, WebSocket upgrade
internal/realtime/   — connection lifecycle, actor loop, tickers, protocol
internal/game/       — authoritative World, movement rules, snapshots, deltas
web/                 — Leaflet/Amap frontend, keyboard + d-pad input
```

The Hub goroutine owns all connections and the World. Client read goroutines
submit input events through a channel. A 20 Hz simulation ticker advances the
World; a separate 10 Hz broadcast ticker sends only accumulated changes and
removals.

## WebSocket Protocol

Client → Server:

```json
{"type":"input","sequence":42,"up":true,"down":false,"left":false,"right":true}
```

Server → Newly Connected Client:

```json
{"type":"world_snapshot","tick":1280,"players":[{"id":"p-…","lat":31.23,"lng":121.47}]}
```

Server → Existing Clients:

```json
{"type":"players_delta","tick":1282,"players":[{"id":"p-…","lat":31.24,"lng":121.48}],"removedPlayerIds":[]}
```
````

Retain the existing Quick Start, custom host/port, and Run Tests sections.

- [ ] **Step 2: Update AGENTS.md**

Change these lines:

```markdown
Go 1.26 项目，服务端权威的 WebSocket 实时移动服务。
```

```text
internal/game/     — World、输入状态、移动模拟和增量状态
internal/realtime/ — Hub actor loop、tick 调度、连接和消息协议
```

Replace the protocol bullet with:

```markdown
- `messages.go` 定义 `input` / `world_snapshot` / `players_delta` 协议
- `game.World` 拥有玩家坐标；客户端只能发送输入状态
- `Hub.Run()` 是唯一 actor loop，按 20 Hz 模拟、10 Hz 增量广播
```

- [ ] **Step 3: Check documentation for obsolete protocol names**

Run:

```bash
rg -n 'position_update|players_snapshot|client-supplied coordinates|player position state' README.md AGENTS.md
```

Expected: no matches.

- [ ] **Step 4: Commit**

Run:

```bash
git add README.md AGENTS.md
git commit -m "docs: describe authoritative movement"
```

---

## Task 7: Automated Verification

**Files:**
- Modify only if verification exposes a concrete defect.

- [ ] **Step 1: Run formatting check**

Run:

```bash
gofmt -d cmd internal
```

Expected: no output.

- [ ] **Step 2: Run tests repeatedly**

Run:

```bash
go test ./... -count=10
```

Expected: PASS on all ten runs. This is especially important for manual-channel
Hub tests.

- [ ] **Step 3: Run static checks**

Run:

```bash
go vet ./...
git diff --check
```

Expected: both commands succeed with no output.

- [ ] **Step 4: Check protocol migration**

Run:

```bash
rg -n 'position_update|players_snapshot|UpdatePosition|NewState|type State struct' --glob '!docs/superpowers/**' .
```

Expected: no matches.

- [ ] **Step 5: Check frontend authority boundary**

Run:

```bash
rg -n '"lat"|"lng"|lat:|lng:' web/app.js
```

Expected: matches are limited to map start position and rendering
`player.lat`/`player.lng`; the outbound `socket.send` object contains no
coordinates.

- [ ] **Step 6: Commit fixes only if needed**

Inspect:

```bash
git status --short
```

Stage only files changed to fix verification defects, then commit:

```bash
git add <specific-files>
git commit -m "fix: stabilize authoritative movement"
```

Do not create an empty commit.

---

## Task 8: Browser And Runtime Verification

**Files:**
- Modify only if verification exposes a concrete defect.

- [ ] **Step 1: Start the server**

Run:

```bash
go run ./cmd/map-walker
```

Expected:

```text
map-walker listening on http://0.0.0.0:8080
```

The server should also print one `realtime stats` line per second.

- [ ] **Step 2: Verify initial snapshot**

Open:

```text
http://localhost:8080
```

Expected:

- Status becomes `Connected`.
- Local marker appears at the server-defined spawn without any client-supplied
  coordinate message.
- Browser console has no errors.

- [ ] **Step 3: Verify authoritative keyboard movement**

Hold `W` for approximately one second, then release.

Expected:

- The browser sends one input transition for key down and one for key up, not a
  stream of coordinate updates.
- Marker movement begins from server delta messages.
- Marker stops after key release.
- Server logs show about 20 simulation ticks and at most 10 delta broadcasts per
  second.

- [ ] **Step 4: Verify focus-loss safety**

Hold a movement key, switch to another tab/window, then return.

Expected: movement stops because blur/visibility handling sends neutral input.

- [ ] **Step 5: Verify mobile direction pad**

Press and hold each direction button using a narrow browser viewport or phone.

Expected:

- Press starts movement.
- Release or pointer cancellation stops movement.
- No browser-side movement interval exists.

- [ ] **Step 6: Verify two-window snapshot and deltas**

Open a second browser window.

Expected:

- The second window receives a full snapshot containing both players.
- The first window learns about the second player on the next delta.
- Holding movement in either window updates both at the broadcast cadence.
- Closing one window removes that marker from the other on the next delta.

- [ ] **Step 7: Verify server authority in DevTools**

Inspect WebSocket frames.

Expected client frames:

```json
{"type":"input","sequence":1,"up":false,"down":false,"left":false,"right":false}
{"type":"input","sequence":2,"up":true,"down":false,"left":false,"right":false}
```

Expected: no client frame contains `lat`, `lng`, or `playerId`.

- [ ] **Step 8: Record observed metrics**

With one continuously moving player, note:

- `simulation_ticks` near 20 per second.
- `delta_broadcasts` near 10 per second.
- `changed_players` near 10 per second.
- `delta_bytes` non-zero.

With all players stationary:

- `simulation_ticks` remains near 20.
- `delta_broadcasts`, `changed_players`, and `delta_bytes` become 0 after pending
  joins/removals are flushed.

- [ ] **Step 9: Commit runtime fixes only if needed**

Run:

```bash
git status --short
```

If files changed, stage only those files and commit:

```bash
git add <specific-files>
git commit -m "fix: correct authoritative runtime behavior"
```

---

## Task 9: Final Handoff

**Files:**
- Modify: `docs/map-walker-handoff.md`

- [ ] **Step 1: Add phase-two implementation status**

Append:

```markdown
## Authoritative Movement Phase

- Clients send directional input with a monotonically increasing sequence.
- `game.World` owns spawn positions, movement speed, coordinates, ticks, and
  dirty/removal tracking.
- Simulation runs at 20 Hz.
- Incremental broadcasts run at up to 10 Hz and skip empty deltas.
- New clients receive `world_snapshot`; existing clients receive
  `players_delta`.
- Frontend movement follows server output and sends neutral input on release,
  blur, and page hide.
- Verification: `go test ./...`, `go vet ./...`, and two-window browser testing.
```

- [ ] **Step 2: Run final checks**

Run:

```bash
go test ./...
go vet ./...
git diff --check
git status --short
```

Expected: tests and checks PASS. Only the intentional handoff edit should remain
before commit.

- [ ] **Step 3: Commit handoff**

Run:

```bash
git add docs/map-walker-handoff.md
git commit -m "docs: update authoritative movement handoff"
```

- [ ] **Step 4: Final response**

Report:

- Commits created.
- Automated test results.
- Browser verification results.
- Observed simulation and delta rates.
- Remaining limitations: no AOI, interpolation, prediction, collision,
  persistence, or multi-node support.
