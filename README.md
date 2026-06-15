# Map Walker

A small server-authoritative multiplayer movement demo built with Go and
Leaflet. Browsers register an account, then send keyboard or touch input; the Go
server owns player positions, simulates movement at 20 Hz, replicates visible
changes at 10 Hz via a grid-based AOI spatial index, and persists positions every
5 seconds. MySQL is the production target backend; SQLite is retained for local
development and testing.

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

### Synthetic clients

Pre-provisioned bot accounts that connect over WebSocket and wander the map,
exercising AOI and replication at scale without needing real users:

```bash
# 50 bots ramp up at 5/s using accounts already in the database
go run ./cmd/map-walker -synthetic-clients 50 -synthetic-ramp-rate 5

# 50 bots with automatic account provisioning (requires admin password)
MAP_WALKER_ADMIN_PASSWORD=secret go run ./cmd/map-walker \
  -synthetic-clients 50 -synthetic-auto-provision
```

| Flag | Default | Description |
|------|---------|-------------|
| `-synthetic-clients N` | `0` (disabled) | Number of synthetic bot clients to maintain |
| `-synthetic-ramp-rate N` | `5` | Connections per second during ramp-up |
| `-synthetic-auto-provision` | `false` | Register bot accounts automatically |

### Admin page

A read-only operator dashboard showing live Hub and synthetic-client metrics.
Enable it by setting `MAP_WALKER_ADMIN_TOKEN` before starting the server:

```bash
MAP_WALKER_ADMIN_TOKEN=my-secret-token go run ./cmd/map-walker \
  -synthetic-clients 50
# open http://localhost:8080/admin — enter the token in the UI
```

The page polls `/api/admin/synthetic-stats` once per second.
The token is kept only in `sessionStorage` (tab-scoped, cleared on tab close)
and never sent to the server outside the `Authorization: Bearer` header.

## Architecture

```text
cmd/map-walker/      — entry point, graceful shutdown
internal/server/     — routes, static files, WebSocket upgrade, auth/appearance endpoints
internal/realtime/   — connection lifecycle, actor loop, tickers, protocol, persistence, replication
internal/game/       — authoritative World, movement rules, AOI spatial index, appearance
internal/auth/       — user registration/login, session tokens, bcrypt
internal/storage/    — SQLite/MySQL, migrations, user/session/position/appearance persistence
web/                 — Leaflet/Amap frontend, auth card, account menu, keyboard + virtual joystick
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
simulation and broadcasts are never blocked. On MySQL, the worker collapses
per-user updates to the highest sequence, then writes in 500-row bulk `UPDATE
... JOIN` chunks with independent transactions per chunk. On SQLite, the worker
uses the existing per-row update path. Genuine disconnects and logout trigger a
synchronous final save. On reconnect, the saved position is restored.
Same-account replacement (e.g. page refresh) keeps the in-memory position.

### Area of Interest (AOI)

A 600m-cell spatial grid with 500m enter / 600m leave hysteresis prevents
boundary flicker. Each player only receives updates for visible neighbours
within range — the 10 Hz replication cost scales with visible players, not
total world population. Movement checks nine neighbouring cells instead of
the full player set.

## WebSocket Protocol

Client → Server:

```json
{"type":"input","sequence":42,"up":true,"down":false,"left":false,"right":true}
```

Server → Newly Connected Client (in order):

```json
{"type":"self_state","tick":1280,"player":{"id":1,"username":"alice","lat":31.23,"lng":121.47,"appearance":{"color":"#3388ff","shape":"circle"}}}
```
```json
{"type":"visible_entities_snapshot","tick":1280,"players":[{"id":2,"username":"bob","lat":31.24,"lng":121.48,"appearance":{"color":"#ff6600","shape":"diamond"}}]}
```

Server → Existing Clients (10 Hz, per-client):

```json
{"type":"replication_update","tick":1282,"positions":[{"id":2,"lat":31.24,"lng":121.48}],"entered":[{"id":3,"username":"carol","lat":31.25,"lng":121.49,"appearance":{...}}],"leftPlayerIds":[4],"appearances":[{"playerId":2,"appearance":{...}}]}
```

Server → All Clients (on appearance change):

```json
{"type":"appearance_changed","playerId":2,"appearance":{"color":"#ff6600","shape":"diamond"}}
```

Player IDs are BIGINT database user IDs — the server ignores client-supplied
player IDs.

## HTTP API

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/register` | No | Register and get session cookie |
| `POST` | `/api/login` | No | Login and get session cookie |
| `POST` | `/api/logout` | No | Revoke session, clear cookie |
| `GET` | `/api/session` | No | Return current user or 401 |
| `PUT` | `/api/appearance` | Session | Update marker color/shape |
| `GET` | `/ws` | Session | WebSocket upgrade |
| `GET` | `/healthz` | No | Health check |
| `GET` | `/admin` | — | Admin dashboard (404 if `MAP_WALKER_ADMIN_TOKEN` unset) |
| `GET` | `/api/admin/synthetic-stats` | Bearer token | Aggregate Hub + synthetic metrics JSON (404 if token unset) |

## Run Tests

```bash
go test ./...
```
