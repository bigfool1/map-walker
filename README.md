# Map Walker

A small server-authoritative multiplayer movement demo built with Go and
Leaflet. Browsers register an account, then send keyboard or touch input; the Go
server owns player positions, simulates movement at 20 Hz, broadcasts changed
players at 10 Hz, and persists positions to SQLite every 5 seconds.

## Quick Start

```bash
go run ./cmd/map-walker
# open http://localhost:8080 — register or log in, then move
```

Custom host/port or MySQL:

```bash
go run ./cmd/map-walker -host 127.0.0.1 -port 3000
go run ./cmd/map-walker -db-driver mysql -db-dsn 'user:pass@tcp(localhost:3306)/mapwalker'
```

Database is created automatically on first run (`data/map-walker.db` for SQLite).
Press **Ctrl+C** for graceful shutdown — all online positions are saved before exit.

Open two browser windows with different accounts (or one account in two windows
for reconnect testing) to see multiplayer.

## Architecture

```text
cmd/map-walker/      — entry point, graceful shutdown
internal/server/     — routes, static files, WebSocket upgrade, auth endpoints
internal/realtime/   — connection lifecycle, actor loop, tickers, protocol, persistence interface
internal/game/       — authoritative World, movement rules, snapshots, deltas
internal/auth/       — user registration/login, session tokens, bcrypt
internal/storage/    — SQLite/MySQL, migrations, user/session/position persistence
web/                 — Leaflet/Amap frontend, auth card, keyboard + virtual joystick
```

### Identity flow

User registers or logs in → server sets `map_walker_session` cookie (HttpOnly,
30-day expiry, SHA-256 hashed in DB) → WebSocket upgrade authenticates from
cookie → Hub uses the authenticated user ID as the player ID. Logout disconnects
the WebSocket, saves the final position, revokes the session, and clears the
cookie.

### Position persistence

Every 5 seconds the Hub submits only moved players to a background
`PersistenceWorker` which writes to the database via a dedicated goroutine —
simulation and broadcasts are never blocked. Genuine disconnects and logout
trigger a synchronous final save. On reconnect, the saved position is restored.
Same-account replacement (e.g. page refresh) keeps the in-memory position.

## WebSocket Protocol

Client → Server:

```json
{"type":"input","sequence":42,"up":true,"down":false,"left":false,"right":true}
```

Server → Newly Connected Client:

```json
{"type":"world_snapshot","tick":1280,"players":[{"id":"UUID","lat":31.23,"lng":121.47}]}
```

Server → Existing Clients:

```json
{"type":"players_delta","tick":1282,"players":[{"id":"UUID","lat":31.24,"lng":121.48}],"removedPlayerIds":[]}
```

Player IDs are authenticated user UUIDs — the server ignores client-supplied
player IDs.

## HTTP API

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/register` | No | Register and get session cookie |
| `POST` | `/api/login` | No | Login and get session cookie |
| `POST` | `/api/logout` | No | Revoke session, clear cookie |
| `GET` | `/api/session` | No | Return current user or 401 |
| `GET` | `/ws` | Session | WebSocket upgrade |
| `GET` | `/healthz` | No | Health check |

## Run Tests

```bash
go test ./...
```
