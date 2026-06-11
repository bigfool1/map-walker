# Map Walker

A real-time multiplayer position-sharing demo built with Go and Leaflet. Each
browser tab controls a player on a shared map. Move with keyboard or on-screen
controls — all connected players see each other move in real time.

## Quick Start

```bash
go run ./cmd/map-walker
# open http://localhost:8080
```

Custom host/port:

```bash
go run ./cmd/map-walker -host 127.0.0.1 -port 3000
```

Open two browser windows (or a phone on the same Wi-Fi) to see multiplayer.

## Architecture

```
cmd/map-walker/      — entry point, wires Hub + HTTP server
internal/server/     — routes, static files, WebSocket upgrade
internal/realtime/   — Hub actor loop, per-client read/write goroutines, message types
internal/game/       — player position state, snapshots
web/                 — Leaflet frontend, keyboard + d-pad controls
```

A single Hub goroutine owns all player state. External goroutines send events
through typed channels (`register`, `unregister`, `update`); the Hub selects on
them in a loop, keeping all mutation in one place without locks.

Each browser connection becomes a Client with two goroutines: `readLoop` pumps
WebSocket messages into the Hub, `writeLoop` drains snapshots from a buffered
channel back to the browser.

## WebSocket Protocol

Client → Server (`position_update`):

```json
{"type":"position_update","playerId":"p-…","lat":31.23,"lng":121.47}
```

Server → All Clients (`players_snapshot`):

```json
{"type":"players_snapshot","players":[{"id":"p-…","lat":31.23,"lng":121.47}]}
```

## Run Tests

```bash
go test ./...
```
