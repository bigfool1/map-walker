# Map Walker

A small server-authoritative multiplayer movement demo built with Go and
Leaflet. Browsers send keyboard or touch input; the Go server owns player
positions, simulates movement at 20 Hz, and broadcasts changed players at 10 Hz.

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

```text
cmd/map-walker/      — entry point, wires Hub + HTTP server
internal/server/     — routes, static files, WebSocket upgrade
internal/realtime/   — connection lifecycle, actor loop, tickers, protocol
internal/game/       — authoritative World, movement rules, snapshots, deltas
web/                 — Leaflet/Amap frontend, keyboard + virtual joystick
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

## Run Tests

```bash
go test ./...
```
