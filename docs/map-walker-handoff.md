# Map Walker Handoff

Server-authoritative multiplayer demo: Go 1.26 + Leaflet. Players move through a
shared world, pick up gold collectibles for permanent score, and compete on an
on-demand online leaderboard. Hub actor owns all state; synthetic bots exercise
AOI/replication at scale.

Design docs and plans are in `docs/superpowers/specs/` and
`docs/superpowers/plans/`. Phase history (AOI, replication fan-out, MySQL batch
persistence, synthetic clients, collectible world) is in git log.

## Quick Start & Test

```bash
go run ./cmd/map-walker                           # http://localhost:8080
go run ./cmd/map-walker -db-driver mysql -db-dsn 'user:pass@tcp(localhost:3306)/mapwalker'
go run ./cmd/map-walker -synthetic-clients 50 -synthetic-ramp-rate 5
go test ./...                                      # all tests
go test -race ./internal/...                       # race detector
go vet ./...                                       # lint
```

## Project Layout

```
cmd/map-walker/       — entrypoint, flags, graceful shutdown
config/               — collectible region JSON
internal/game/        — World, AOI, CollectibleField (pure logic, no I/O)
internal/realtime/    — Hub actor, Client, messages, replication, persistence/score ifaces
internal/server/      — HTTP routes, WebSocket upgrade, auth/appearance/leaderboard endpoints
internal/auth/        — registration/login, sessions, bcrypt, synthetic identity
internal/storage/     — SQLite/MySQL, forward migrations, user/session/pos/score persistence
internal/synthetic/   — bot Client, Manager actor, Provisioner
web/                  — Leaflet frontend, auth card, appearance editor, gems, leaderboard
```

## Core Conventions

- **Hub.Run() is the only actor** — all state mutation inside its select loop. No locks elsewhere.
- **AOI**: 600m cells, 500m enter / 600m leave hysteresis, 9-cell scan. Collectibles use same grid.
- **Ticks**: 20 Hz sim, 10 Hz broadcast, 5s position persistence, 1s stats.
- **Persistence**: async Submit for periodic saves; sync SubmitSync for disconnect/logout/shutdown. ScorePersister coalesces to highest score, retries with exponential backoff (cap 30s).
- **Protocol**: 4 init messages (self_state + score, visible_entities_snapshot, collectible_regions, visible_collectibles_snapshot). replication_update carries player + collectible deltas.
- **Synthetic identity**: `is_synthetic` is a DB column, carried through auth → WebSocket → Hub. Client cannot control it. Synthetic players move but can't collect or rank.
- **Pickup validation**: serial in Hub actor — connection check, synthetic reject, 300ms cooldown, existence, visibility, ≤10m distance. No DB call in critical path.
- **Leaderboard**: on-demand HTTP → Hub actor query. Filters synthetic/offline, O(P log P) sort. No cache/poll/push.
- **Tests**: `internal/game/` pure unit; `internal/realtime/` use `testClient` + manual tick channels; `internal/server/` use `httptest.NewServer` + SQLite; scale tests in `*_scale_test.go`.

## Database

- SQLite default (`data/map-walker.db`), MySQL via `-db-driver mysql`.
- Forward-only migrations: `001_initial.sql`, `002_collectibles.sql` (score + is_synthetic).
- Tables: `users` (id, username, password_hash, last_lat/lng, appearance, collectible_score, is_synthetic), `sessions` (token_hash, user_id, expires_at).

## WebSocket Messages

| Direction | Type | Purpose |
|-----------|------|---------|
| C→S | `input` | Direction keys (sequence, up/down/left/right) |
| C→S | `collect` | Pickup intent (collectibleId) |
| S→C (init) | `self_state` | Player identity, position, appearance, score |
| S→C (init) | `visible_entities_snapshot` | AOI-filtered nearby players |
| S→C (init) | `collectible_regions` | Public region geometry (no respawn timing) |
| S→C (init) | `visible_collectibles_snapshot` | AOI-filtered visible gems |
| S→C (10Hz) | `replication_update` | Player + collectible deltas (entered/left/spawned/collected) |
| S→C (winner) | `collect_result` | Pickup success (collectibleId, new score) |

## HTTP API

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/api/register` | — | Register, get session cookie |
| POST | `/api/login` | — | Login, get session cookie |
| POST | `/api/logout` | — | Revoke session, clear cookie |
| GET | `/api/session` | — | Current user or 401 |
| PUT | `/api/appearance` | Session | Update marker color/shape |
| GET | `/api/leaderboard/online` | Session | Online Top 5 + self rank |
| GET | `/ws` | Session | WebSocket upgrade |
| GET | `/admin` | Bearer | Dashboard (404 if token unset) |
| GET | `/api/admin/synthetic-stats` | Bearer | Hub + synthetic metrics JSON |

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-host` / `-port` | `0.0.0.0` / `8080` | Listen address |
| `-db-driver` / `-db-dsn` | `sqlite` / `data/map-walker.db` | Database backend |
| `-collectible-regions` | `config/collectible-regions.json` | Region config |
| `-synthetic-clients` | `0` | Bot count |
| `-synthetic-ramp-rate` | `10` | Connections/sec during ramp-up |
| `-synthetic-auto-provision` | `false` | Auto-create bot accounts |

## Known Limitations

- Collectible instances reset on restart (scores persist).
- Score durability is bounded — process kill may lose unpersisted points.
- Leaderboard is online-only; no offline player scores.
- Single Hub owns all state; no multi-process coordination.
- Region config is fixed at startup; no runtime reload.
- No password recovery, email verification, or OAuth.
- Map tiles depend on Gaode CDN; Leaflet from unpkg CDN.
- Graceful shutdown timeout is 10s.
- Synthetic manager/stats tests currently fail (pre-existing, unrelated to collectibles).
