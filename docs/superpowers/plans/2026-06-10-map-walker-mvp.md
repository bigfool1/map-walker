# Map Walker MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a small Go + browser map walking demo where multiple browser windows connect over WebSocket and see each other's player markers move.

**Architecture:** The Go backend serves static files and owns a WebSocket Hub. The Hub is the single owner of connected clients and in-memory player state; clients have separate read and write loops. The frontend is plain HTML/CSS/JS using Leaflet with OpenStreetMap tiles, keyboard movement, and a lightweight mobile directional pad.

**Tech Stack:** Go `net/http`, `github.com/coder/websocket`, Go tests, plain JavaScript, Leaflet, OpenStreetMap tiles.

---

## File Structure

- Create: `go.mod`
  - Module definition. Use module path `map-walker` for a local learning project.
- Create: `cmd/map-walker/main.go`
  - Program entrypoint. Starts the Hub, builds the HTTP server, and listens on `:8080`.
- Create: `internal/game/state.go`
  - In-memory player position state and snapshot helpers.
- Create: `internal/game/state_test.go`
  - Tests for update, remove, and snapshot behavior.
- Create: `internal/realtime/messages.go`
  - WebSocket message structs and constants shared by Hub, Client, and tests.
- Create: `internal/realtime/messages_test.go`
  - Tests for JSON encode/decode stability.
- Create: `internal/realtime/hub.go`
  - Single event loop for register, unregister, position updates, and broadcasts.
- Create: `internal/realtime/hub_test.go`
  - Tests Hub register, unregister, update, and slow-client behavior without a real browser.
- Create: `internal/realtime/client.go`
  - One WebSocket connection wrapper with read loop and write loop.
- Create: `internal/server/server.go`
  - HTTP routes for `/`, static assets, `/healthz`, and `/ws`.
- Create: `web/index.html`
  - Browser shell and Leaflet imports.
- Create: `web/app.js`
  - Map setup, WebSocket connection, marker rendering, keyboard controls, mobile controls.
- Create: `web/styles.css`
  - Full-screen map, status badge, and virtual direction pad.

Use explanatory comments in code around:

- Hub channel event loop, comparing it to a Python `asyncio.Queue` consumer.
- Client read/write loop split, explaining WebSocket write serialization.
- Per-client send channel, explaining slow-client backpressure.
- Frontend input normalization, explaining keyboard and mobile controls becoming the same movement command.

Keep routine code lightly commented.

---

## Task 1: Initialize Go Module And Dependency

**Files:**
- Create: `go.mod`

- [ ] **Step 1: Initialize module**

Run:

```bash
go mod init map-walker
```

Expected: `go.mod` exists and contains:

```go
module map-walker

go 1.23.0
```

If the local Go version writes a different `go` directive such as `go 1.22.0` or `go 1.24.0`, keep the generated value.

- [ ] **Step 2: Add WebSocket dependency**

Run:

```bash
go get github.com/coder/websocket@latest
```

Expected: `go.mod` includes `github.com/coder/websocket`, and `go.sum` is created.

If network access is blocked, rerun with the required sandbox/network approval. Do not replace the dependency with ad-hoc WebSocket code.

- [ ] **Step 3: Verify empty module**

Run:

```bash
go test ./...
```

Expected: command succeeds with output similar to:

```text
go: warning: "./..." matched no packages
```

- [ ] **Step 4: Commit**

Run:

```bash
git add go.mod go.sum
git commit -m "chore: initialize go module"
```

---

## Task 2: Add Game State With Tests

**Files:**
- Create: `internal/game/state.go`
- Create: `internal/game/state_test.go`

- [ ] **Step 1: Write failing state tests**

Create `internal/game/state_test.go`:

```go
package game

import "testing"

func TestStateUpdateAndSnapshot(t *testing.T) {
	state := NewState()

	state.UpdatePosition(PlayerPosition{ID: "alice", Lat: 31.2304, Lng: 121.4737})
	state.UpdatePosition(PlayerPosition{ID: "bob", Lat: 31.2310, Lng: 121.4740})

	snapshot := state.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 players, got %d", len(snapshot))
	}

	players := map[string]PlayerPosition{}
	for _, player := range snapshot {
		players[player.ID] = player
	}

	if players["alice"].Lat != 31.2304 || players["alice"].Lng != 121.4737 {
		t.Fatalf("alice position was not preserved: %+v", players["alice"])
	}
	if players["bob"].Lat != 31.2310 || players["bob"].Lng != 121.4740 {
		t.Fatalf("bob position was not preserved: %+v", players["bob"])
	}
}

func TestStateUpdateReplacesExistingPlayer(t *testing.T) {
	state := NewState()

	state.UpdatePosition(PlayerPosition{ID: "alice", Lat: 1, Lng: 2})
	state.UpdatePosition(PlayerPosition{ID: "alice", Lat: 3, Lng: 4})

	snapshot := state.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 player, got %d", len(snapshot))
	}
	if snapshot[0].Lat != 3 || snapshot[0].Lng != 4 {
		t.Fatalf("expected updated position, got %+v", snapshot[0])
	}
}

func TestStateRemove(t *testing.T) {
	state := NewState()

	state.UpdatePosition(PlayerPosition{ID: "alice", Lat: 1, Lng: 2})
	state.RemovePlayer("alice")

	snapshot := state.Snapshot()
	if len(snapshot) != 0 {
		t.Fatalf("expected no players, got %+v", snapshot)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/game
```

Expected: FAIL because `NewState` and `PlayerPosition` are undefined.

- [ ] **Step 3: Implement state**

Create `internal/game/state.go`:

```go
package game

import "sort"

// PlayerPosition is the tiny piece of game state this MVP synchronizes.
// In a larger game this would likely grow into player movement state, avatar
// state, animation state, or room/world membership.
type PlayerPosition struct {
	ID  string  `json:"id"`
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type State struct {
	players map[string]PlayerPosition
}

func NewState() *State {
	return &State{
		players: map[string]PlayerPosition{},
	}
}

func (s *State) UpdatePosition(position PlayerPosition) {
	s.players[position.ID] = position
}

func (s *State) RemovePlayer(playerID string) {
	delete(s.players, playerID)
}

func (s *State) Snapshot() []PlayerPosition {
	players := make([]PlayerPosition, 0, len(s.players))
	for _, player := range s.players {
		players = append(players, player)
	}

	sort.Slice(players, func(i, j int) bool {
		return players[i].ID < players[j].ID
	})

	return players
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/game
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/game/state.go internal/game/state_test.go
git commit -m "feat: add player position state"
```

---

## Task 3: Add WebSocket Message Types With Tests

**Files:**
- Create: `internal/realtime/messages.go`
- Create: `internal/realtime/messages_test.go`

- [ ] **Step 1: Write failing message tests**

Create `internal/realtime/messages_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/realtime
```

Expected: FAIL because message types are undefined.

- [ ] **Step 3: Implement message types**

Create `internal/realtime/messages.go`:

```go
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
```

- [ ] **Step 4: Run tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/realtime/messages.go internal/realtime/messages_test.go
git commit -m "feat: add realtime message types"
```

---

## Task 4: Add Hub Event Loop With Tests

**Files:**
- Create: `internal/realtime/hub.go`
- Create: `internal/realtime/hub_test.go`

- [ ] **Step 1: Write failing Hub tests**

Create `internal/realtime/hub_test.go`:

```go
package realtime

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHubRegistersClientAndSendsInitialSnapshot(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	client := NewTestClient("alice", 4)
	hub.Register(client)

	msg := mustReceiveSnapshot(t, client)
	if len(msg.Players) != 0 {
		t.Fatalf("expected empty initial snapshot, got %+v", msg.Players)
	}
}

func TestHubBroadcastsPositionUpdates(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 4)
	bob := NewTestClient("bob", 4)
	hub.Register(alice)
	hub.Register(bob)
	mustReceiveSnapshot(t, alice)
	mustReceiveSnapshot(t, bob)

	hub.UpdatePosition(PositionUpdateMessage{
		Type:     MessageTypePositionUpdate,
		PlayerID: "alice",
		Lat:      31.2304,
		Lng:      121.4737,
	})

	aliceSnapshot := mustReceiveSnapshot(t, alice)
	bobSnapshot := mustReceiveSnapshot(t, bob)

	for _, snapshot := range []PlayersSnapshotMessage{aliceSnapshot, bobSnapshot} {
		if len(snapshot.Players) != 1 {
			t.Fatalf("expected 1 player, got %+v", snapshot.Players)
		}
		if snapshot.Players[0].ID != "alice" {
			t.Fatalf("expected alice, got %+v", snapshot.Players[0])
		}
	}
}

func TestHubUnregisterRemovesPlayerAndBroadcasts(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient("alice", 4)
	bob := NewTestClient("bob", 4)
	hub.Register(alice)
	hub.Register(bob)
	mustReceiveSnapshot(t, alice)
	mustReceiveSnapshot(t, bob)

	hub.UpdatePosition(PositionUpdateMessage{Type: MessageTypePositionUpdate, PlayerID: "alice", Lat: 1, Lng: 2})
	mustReceiveSnapshot(t, alice)
	mustReceiveSnapshot(t, bob)

	hub.Unregister(alice)

	msg := mustReceiveSnapshot(t, bob)
	if len(msg.Players) != 0 {
		t.Fatalf("expected alice to be removed, got %+v", msg.Players)
	}
}

func TestHubDropsSlowClient(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	slow := NewTestClient("slow", 0)
	fast := NewTestClient("fast", 8)
	hub.Register(slow)
	hub.Register(fast)
	mustReceiveSnapshot(t, fast)

	hub.UpdatePosition(PositionUpdateMessage{Type: MessageTypePositionUpdate, PlayerID: "fast", Lat: 1, Lng: 2})
	mustReceiveSnapshot(t, fast)

	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("expected slow client to be closed")
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

func mustReceiveSnapshot(t *testing.T, client *testClient) PlayersSnapshotMessage {
	t.Helper()

	select {
	case data := <-client.send:
		var msg PlayersSnapshotMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("decode snapshot failed: %v", err)
		}
		if msg.Type != MessageTypePlayersSnapshot {
			t.Fatalf("expected snapshot message, got %q", msg.Type)
		}
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
		return PlayersSnapshotMessage{}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/realtime
```

Expected: FAIL because `NewHub`, `Register`, `UpdatePosition`, and related types are undefined.

- [ ] **Step 3: Implement Hub**

Create `internal/realtime/hub.go`:

```go
package realtime

import (
	"encoding/json"

	"map-walker/internal/game"
)

type ClientSender interface {
	ID() string
	Send([]byte) bool
	CloseSend()
}

type Hub struct {
	state      *game.State
	register   chan ClientSender
	unregister chan ClientSender
	updates    chan PositionUpdateMessage
	stop       chan struct{}
	clients    map[string]ClientSender
}

func NewHub() *Hub {
	return &Hub{
		state:      game.NewState(),
		register:   make(chan ClientSender),
		unregister: make(chan ClientSender),
		updates:    make(chan PositionUpdateMessage),
		stop:       make(chan struct{}),
		clients:    map[string]ClientSender{},
	}
}

// Run is the backend's tiny "world loop".
//
// Python comparison: this looks like one long-running asyncio task that owns an
// asyncio.Queue. Other goroutines send events into channels; this goroutine is
// the only place that mutates clients and player state, so we avoid sprinkling
// locks through the rest of the code.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client.ID()] = client
			h.sendSnapshot(client)
		case client := <-h.unregister:
			h.removeClient(client)
			h.broadcastSnapshot()
		case update := <-h.updates:
			h.state.UpdatePosition(game.PlayerPosition{
				ID:  update.PlayerID,
				Lat: update.Lat,
				Lng: update.Lng,
			})
			h.broadcastSnapshot()
		case <-h.stop:
			for _, client := range h.clients {
				client.CloseSend()
			}
			return
		}
	}
}

func (h *Hub) Stop() {
	close(h.stop)
}

func (h *Hub) Register(client ClientSender) {
	h.register <- client
}

func (h *Hub) Unregister(client ClientSender) {
	h.unregister <- client
}

func (h *Hub) UpdatePosition(update PositionUpdateMessage) {
	h.updates <- update
}

func (h *Hub) removeClient(client ClientSender) {
	if _, ok := h.clients[client.ID()]; !ok {
		return
	}
	delete(h.clients, client.ID())
	h.state.RemovePlayer(client.ID())
	client.CloseSend()
}

func (h *Hub) sendSnapshot(client ClientSender) {
	data := h.snapshotData()
	if ok := client.Send(data); !ok {
		h.removeClient(client)
	}
}

func (h *Hub) broadcastSnapshot() {
	data := h.snapshotData()
	for _, client := range h.clients {
		if ok := client.Send(data); !ok {
			h.removeClient(client)
		}
	}
}

func (h *Hub) snapshotData() []byte {
	msg := NewPlayersSnapshotMessage(h.state.Snapshot())
	data, err := json.Marshal(msg)
	if err != nil {
		panic(err)
	}
	return data
}
```

- [ ] **Step 4: Run Hub tests**

Run:

```bash
go test ./internal/realtime
```

Expected: PASS.

- [ ] **Step 5: Run all tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/realtime/hub.go internal/realtime/hub_test.go
git commit -m "feat: add realtime hub"
```

---

## Task 5: Add WebSocket Client Read And Write Loops

**Files:**
- Create: `internal/realtime/client.go`

- [ ] **Step 1: Implement client**

Create `internal/realtime/client.go`:

```go
package realtime

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const sendBufferSize = 16

type Client struct {
	id        string
	conn      *websocket.Conn
	hub       *Hub
	send      chan []byte
	closeOnce sync.Once
}

func NewClient(id string, conn *websocket.Conn, hub *Hub) *Client {
	return &Client{
		id:   id,
		conn: conn,
		hub:  hub,
		send: make(chan []byte, sendBufferSize),
	}
}

func (c *Client) ID() string {
	return c.id
}

func (c *Client) Send(data []byte) bool {
	select {
	case c.send <- data:
		return true
	default:
		// Backpressure lesson: this is like an asyncio.Queue with maxsize.
		// If the browser cannot drain messages, we prefer dropping the
		// connection over letting memory grow forever.
		return false
	}
}

func (c *Client) CloseSend() {
	c.closeOnce.Do(func() {
		close(c.send)
	})
}

func (c *Client) Run(ctx context.Context) {
	c.hub.Register(c)
	defer c.hub.Unregister(c)

	go c.writeLoop(ctx)
	c.readLoop(ctx)
}

func (c *Client) readLoop(ctx context.Context) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}

		var msg PositionUpdateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("decode websocket message failed: %v", err)
			continue
		}
		if msg.Type != MessageTypePositionUpdate {
			continue
		}

		msg.PlayerID = c.id
		c.hub.UpdatePosition(msg)
	}
}

func (c *Client) writeLoop(ctx context.Context) {
	// WebSocket writes are kept in one goroutine. This mirrors the common Python
	// rule of making one task responsible for socket writes so concurrent sends
	// do not interleave frames or fight over connection state.
	for data := range c.send {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.conn.Write(writeCtx, websocket.MessageText, data)
		cancel()
		if err != nil {
			return
		}
	}
}
```

- [ ] **Step 2: Run tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run tests again**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git add internal/realtime/client.go
git commit -m "feat: add websocket client loops"
```

---

## Task 6: Add HTTP Server And Entrypoint

**Files:**
- Create: `internal/server/server.go`
- Create: `cmd/map-walker/main.go`

- [ ] **Step 1: Implement HTTP server**

Create `internal/server/server.go`:

```go
package server

import (
	"context"
	"net/http"

	"map-walker/internal/realtime"

	"github.com/coder/websocket"
)

type Server struct {
	hub    *realtime.Hub
	static http.Handler
}

func New(hub *realtime.Hub) *Server {
	return &Server{
		hub:    hub,
		static: http.FileServer(http.Dir("web")),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.Handle("/", s.static)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	playerID := r.URL.Query().Get("playerId")
	if playerID == "" {
		http.Error(w, "playerId is required", http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}

	client := realtime.NewClient(playerID, conn, s.hub)
	client.Run(context.Background())

	_ = conn.Close(websocket.StatusNormalClosure, "connection closed")
}
```

- [ ] **Step 2: Implement main**

Create `cmd/map-walker/main.go`:

```go
package main

import (
	"log"
	"net/http"

	"map-walker/internal/realtime"
	"map-walker/internal/server"
)

func main() {
	hub := realtime.NewHub()
	go hub.Run()

	srv := server.New(hub)

	addr := ":8080"
	log.Printf("map-walker listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 3: Run tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git add internal/server/server.go cmd/map-walker/main.go
git commit -m "feat: add http server"
```

---

## Task 7: Add Frontend Map And WebSocket Rendering

**Files:**
- Create: `web/index.html`
- Create: `web/app.js`
- Create: `web/styles.css`

- [ ] **Step 1: Create HTML shell**

Create `web/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Map Walker</title>
    <link
      rel="stylesheet"
      href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css"
      integrity="sha256-p4NxAoJBhIINfQnc8laV0nRXz90pWfmkPmCm6I04xyI="
      crossorigin=""
    >
    <link rel="stylesheet" href="/styles.css">
  </head>
  <body>
    <main id="map" aria-label="Map Walker play area"></main>
    <div id="status" class="status status--connecting">Connecting</div>
    <div class="dpad" aria-label="Movement controls">
      <button class="dpad__button dpad__button--up" data-move="up" aria-label="Move up">↑</button>
      <button class="dpad__button dpad__button--left" data-move="left" aria-label="Move left">←</button>
      <button class="dpad__button dpad__button--right" data-move="right" aria-label="Move right">→</button>
      <button class="dpad__button dpad__button--down" data-move="down" aria-label="Move down">↓</button>
    </div>
    <script
      src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js"
      integrity="sha256-20nQCchB9co0qIjJZRGuk2/Z9VM+kNiyxNV1lvTlZBo="
      crossorigin=""
    ></script>
    <script src="/app.js"></script>
  </body>
</html>
```

- [ ] **Step 2: Create frontend JavaScript**

Create `web/app.js`:

```javascript
const startPosition = { lat: 31.2304, lng: 121.4737 };
const playerId = getOrCreatePlayerId();
const markers = new Map();
let currentPosition = { ...startPosition };
let socket = null;

const map = L.map("map", { zoomControl: true }).setView(
  [startPosition.lat, startPosition.lng],
  16
);

L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
  maxZoom: 19,
  attribution: "&copy; OpenStreetMap contributors",
}).addTo(map);

connect();
bindKeyboardControls();
bindDpadControls();

function getOrCreatePlayerId() {
  const key = "map-walker-player-id";
  const existing = sessionStorage.getItem(key);
  if (existing) {
    return existing;
  }
  const created = `p-${crypto.randomUUID()}`;
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
    sendPosition();
  });

  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (message.type === "players_snapshot") {
      renderPlayers(message.players);
    }
  });

  socket.addEventListener("close", () => {
    setStatus("disconnected");
  });

  socket.addEventListener("error", () => {
    setStatus("disconnected");
  });
}

function movePlayer(deltaLat, deltaLng) {
  currentPosition = {
    lat: currentPosition.lat + deltaLat,
    lng: currentPosition.lng + deltaLng,
  };
  map.panTo([currentPosition.lat, currentPosition.lng], { animate: true });
  sendPosition();
}

function sendPosition() {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  socket.send(
    JSON.stringify({
      type: "position_update",
      playerId,
      lat: currentPosition.lat,
      lng: currentPosition.lng,
    })
  );
}

function renderPlayers(players) {
  const liveIds = new Set(players.map((player) => player.id));

  for (const [id, marker] of markers.entries()) {
    if (!liveIds.has(id)) {
      marker.remove();
      markers.delete(id);
    }
  }

  for (const player of players) {
    const marker = markers.get(player.id);
    const label = player.id === playerId ? "You" : "Player";
    if (marker) {
      marker.setLatLng([player.lat, player.lng]);
    } else {
      markers.set(
        player.id,
        L.marker([player.lat, player.lng]).addTo(map).bindTooltip(label)
      );
    }
  }
}

function bindKeyboardControls() {
  window.addEventListener("keydown", (event) => {
    const step = 0.00015;
    if (event.key === "ArrowUp" || event.key.toLowerCase() === "w") {
      movePlayer(step, 0);
    } else if (event.key === "ArrowDown" || event.key.toLowerCase() === "s") {
      movePlayer(-step, 0);
    } else if (event.key === "ArrowLeft" || event.key.toLowerCase() === "a") {
      movePlayer(0, -step);
    } else if (event.key === "ArrowRight" || event.key.toLowerCase() === "d") {
      movePlayer(0, step);
    }
  });
}

function bindDpadControls() {
  // Input-normalization lesson: keyboard and mobile buttons both become calls
  // to movePlayer(). The server never needs to know which device created the
  // movement, similar to normalizing HTTP clients before business logic in a
  // Python backend.
  const step = 0.00015;
  const moves = {
    up: [step, 0],
    down: [-step, 0],
    left: [0, -step],
    right: [0, step],
  };

  for (const button of document.querySelectorAll("[data-move]")) {
    let timer = null;
    const direction = button.dataset.move;
    const [deltaLat, deltaLng] = moves[direction];

    const start = (event) => {
      event.preventDefault();
      movePlayer(deltaLat, deltaLng);
      timer = window.setInterval(() => movePlayer(deltaLat, deltaLng), 120);
    };
    const stop = () => {
      if (timer !== null) {
        window.clearInterval(timer);
        timer = null;
      }
    };

    button.addEventListener("pointerdown", start);
    button.addEventListener("pointerup", stop);
    button.addEventListener("pointercancel", stop);
    button.addEventListener("pointerleave", stop);
  }
}

function setStatus(status) {
  const element = document.getElementById("status");
  element.textContent = status.charAt(0).toUpperCase() + status.slice(1);
  element.className = `status status--${status}`;
}
```

- [ ] **Step 3: Create CSS**

Create `web/styles.css`:

```css
html,
body,
#map {
  height: 100%;
  margin: 0;
}

body {
  font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  overflow: hidden;
}

.status {
  position: fixed;
  top: 12px;
  left: 50%;
  z-index: 1000;
  transform: translateX(-50%);
  border: 1px solid rgba(0, 0, 0, 0.18);
  border-radius: 8px;
  padding: 6px 10px;
  background: rgba(255, 255, 255, 0.94);
  color: #1b1f23;
  font-size: 14px;
  font-weight: 650;
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.16);
}

.status--connected {
  color: #116329;
}

.status--connecting {
  color: #7a4d00;
}

.status--disconnected {
  color: #9a1b1b;
}

.dpad {
  position: fixed;
  right: 18px;
  bottom: 18px;
  z-index: 1000;
  display: grid;
  grid-template-columns: 56px 56px 56px;
  grid-template-rows: 56px 56px 56px;
  gap: 6px;
  touch-action: none;
}

.dpad__button {
  width: 56px;
  height: 56px;
  border: 1px solid rgba(0, 0, 0, 0.22);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.92);
  color: #1b1f23;
  font-size: 24px;
  font-weight: 700;
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.18);
}

.dpad__button:active {
  background: #dceeff;
}

.dpad__button--up {
  grid-column: 2;
  grid-row: 1;
}

.dpad__button--left {
  grid-column: 1;
  grid-row: 2;
}

.dpad__button--right {
  grid-column: 3;
  grid-row: 2;
}

.dpad__button--down {
  grid-column: 2;
  grid-row: 3;
}
```

- [ ] **Step 4: Run Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add web/index.html web/app.js web/styles.css
git commit -m "feat: add browser map frontend"
```

---

## Task 8: Run Local Server And Manual Verification

**Files:**
- Modify only if verification exposes bugs.

- [ ] **Step 1: Start server**

Run:

```bash
go run ./cmd/map-walker
```

Expected:

```text
map-walker listening on http://localhost:8080
```

Keep this command running.

- [ ] **Step 2: Open browser**

Open:

```text
http://localhost:8080
```

Expected:

- Real map loads.
- Status changes to `Connected`.
- One marker appears after the first movement or initial send.
- Direction pad appears in the bottom-right.

- [ ] **Step 3: Verify desktop controls**

In the first browser window:

- Press `W`, `A`, `S`, `D`.
- Press arrow keys.

Expected:

- Local marker moves.
- Map pans with the local player.
- No JavaScript console errors.

- [ ] **Step 4: Verify multiplayer sync**

Open a second browser window at:

```text
http://localhost:8080
```

Expected:

- Both windows show markers.
- Moving window A updates marker in window B.
- Moving window B updates marker in window A.

- [ ] **Step 5: Verify mobile-style controls**

Use one of these:

- Browser responsive design mode with a narrow viewport.
- A real phone on the same network, if the server is reachable from that device.
- Desktop mouse clicking and holding the direction pad buttons.

Expected:

- Direction pad buttons move the local player.
- Holding a button repeats movement.
- Backend receives the same `position_update` messages as keyboard movement.

- [ ] **Step 6: Fix any verification bugs**

If the browser cannot connect to `/ws`, check:

- Browser console WebSocket URL.
- Server log.
- Whether `playerId` query parameter is present.
- Whether `github.com/coder/websocket.Accept` rejected origin settings.

If Leaflet assets fail to load, check:

- Network access to `unpkg.com`.
- Network access to `tile.openstreetmap.org`.
- Browser console for integrity or CORS errors.

If OpenStreetMap tiles are slow in the local network environment, keep the implementation as-is for MVP and document that a later task can swap the provider to 高德 or another tile source.

- [ ] **Step 7: Commit verification fixes**

If code changed:

```bash
git status --short
git add <files changed during verification>
git commit -m "fix: stabilize local map demo"
```

If no code changed, do not create an empty commit.

---

## Task 9: Final Verification And Handoff Notes

**Files:**
- Modify: `docs/map-walker-handoff.md`

- [ ] **Step 1: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Check git status**

Run:

```bash
git status --short
```

Expected: no unexpected changes. If `docs/map-walker-handoff.md` is still untracked from before git initialization, either leave it untracked or explicitly ask the user whether to add it. Do not silently include unrelated files.

- [ ] **Step 3: Update handoff only if requested**

If the user wants the handoff updated, add a short section to `docs/map-walker-handoff.md`:

```markdown
## Implementation Status

- Go module initialized.
- Backend state, message, Hub, Client, and HTTP server implemented.
- Browser frontend implemented with Leaflet, OpenStreetMap tiles, keyboard controls, and mobile direction pad.
- Verification command: `go test ./...`.
- Manual verification target: `http://localhost:8080`.
```

- [ ] **Step 4: Final response**

Summarize:

- Files created.
- Tests run and result.
- Manual browser verification result.
- Local URL.
- Any known limitation, especially OpenStreetMap tile availability and lack of production heartbeat/reconnect.
