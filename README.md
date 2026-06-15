# Map Walker

A small server-authoritative multiplayer demo built with Go and Leaflet. Players
register an account, move through a shared world, pick up gold collectibles for
permanent score points, and compete on an on-demand online leaderboard. The Go
server owns all state: positions, collectibles, scores. Movement simulates at
20 Hz, AOI-filtered replication broadcasts at 10 Hz, and positions persist every
5 seconds. MySQL is the production target backend; SQLite is retained for local
development and testing.

## Quick Start

```bash
go run ./cmd/map-walker
# open http://localhost:8080 — register or log in, then move
```

Custom host/port, MySQL, or collectible region configuration:

```bash
go run ./cmd/map-walker -host 127.0.0.1 -port 3000
go run ./cmd/map-walker -db-driver mysql -db-dsn 'user:pass@tcp(localhost:3306)/mapwalker'
go run ./cmd/map-walker -collectible-regions my-regions.json
```

| Flag | Default | Description |
|------|---------|-------------|
| `-collectible-regions` | `config/collectible-regions.json` | Collectible region configuration file |

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

# 50 bots with automatic account provisioning
MAP_WALKER_SYNTHETIC_PASSWORD=secret go run ./cmd/map-walker \
  -synthetic-clients 50 -synthetic-auto-provision
```

| Flag | Default | Description |
|------|---------|-------------|
| `-synthetic-clients N` | `0` (disabled) | Number of synthetic bot clients to maintain |
| `-synthetic-ramp-rate N` | `5` | Connections per second during ramp-up |
| `-synthetic-auto-provision` | `false` | Register bot accounts automatically |

### Admin page

A read-only operator dashboard showing live Hub and synthetic-client metrics.
Always available at `/stats`:

```bash
go run ./cmd/map-walker -synthetic-clients 50
# open http://localhost:8080/stats
```

The page polls `/api/stats/synthetic` once per second.

## Collectible Gameplay

Twenty translucent gold circular regions are displayed on the map. Each region
contains 5 collectible gems rendered as glowing gold points. Walk within 10
meters of a gem — the nearest one highlights — and press `J` (desktop) or tap
the circular pickup button (touch, lower-right corner) to collect it.

Each successful pickup awards exactly **one permanent score point**. The server
validates every pickup: the collectible must exist, be visible, and the
authoritative distance must be ≤10 meters. Scores are persisted asynchronously
without blocking gameplay; on disconnect or shutdown the latest score is
submitted synchronously.

Collected items respawn after a random 5-15 second delay in the same region.
Collectible instances are in-memory only — a server restart creates fresh items
while preserving player scores.

### Score and Leaderboard

Your permanent score appears between the connection status bar and the map.
Open the **排行** (leaderboard) button to see the current Top 5 online players
and your rank. The leaderboard computes rankings on demand — no polling,
caching, or push updates. Synthetic (bot) accounts are excluded.

### Synthetic Exclusion

Synthetic clients move through the same world and are visible on the map, but
they cannot collect items or appear on the leaderboard. The `is_synthetic`
flag is a persistent, server-trusted user property set during account
provisioning.

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
{"type":"collect","collectibleId":123}
```

Server → Newly Connected Client (in order, 4 messages):

```json
{"type":"self_state","tick":1280,"player":{...},"score":42}
{"type":"visible_entities_snapshot","tick":1280,"players":[...]}
{"type":"collectible_regions","tick":1280,"regions":[{"id":"region-1","centerLat":31.2304,"centerLng":121.4737,"radiusMeters":200}]}
{"type":"visible_collectibles_snapshot","tick":1280,"collectibles":[{"id":1,"regionId":"region-1","lat":31.2305,"lng":121.4738}]}
```

Server → Existing Clients (10 Hz, per-client):

```json
{"type":"replication_update","tick":1282,"positions":[...],"entered":[...],"leftPlayerIds":[...],"appearances":[...],"collectiblesEntered":[...],"collectibleIdsLeft":[...],"collectiblesSpawned":[...],"collectibleIdsCollected":[...]}
```

Server → Winner (on successful pickup):

```json
{"type":"collect_result","collectibleId":123,"score":43}
```

Player IDs are BIGINT database user IDs — the server ignores client-supplied
player IDs, positions, scores, and synthetic identity.

## HTTP API

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/register` | No | Register and get session cookie |
| `POST` | `/api/login` | No | Login and get session cookie |
| `POST` | `/api/logout` | No | Revoke session, clear cookie |
| `GET` | `/api/session` | No | Return current user or 401 |
| `PUT` | `/api/appearance` | Session | Update marker color/shape |
| `GET` | `/api/leaderboard/online` | Session | Online Top 5 + self rank |
| `GET` | `/ws` | Session | WebSocket upgrade |
| `GET` | `/healthz` | No | Health check |
| `GET` | `/stats` | — | Stats dashboard |
| `GET` | `/api/stats/synthetic` | — | Aggregate Hub + synthetic metrics JSON |

## Run Tests

```bash
go test ./...
```
