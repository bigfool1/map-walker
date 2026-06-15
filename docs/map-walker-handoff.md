# Map Walker Handoff

Server-authoritative multiplayer demo: Go 1.26 + Leaflet. Players move through a
shared world, pick up gold collectibles for permanent score, and compete on an
on-demand online leaderboard. Hub actor owns all state; synthetic bots exercise
AOI/replication at scale.

Design docs and plans are in `docs/superpowers/specs/` and
`docs/superpowers/plans/`. Earlier phase history is in git log.

**Most recently completed plan:**
`docs/superpowers/plans/2026-06-15-persistent-collectible-world.md` — all 11
tasks done.

### Collectible field (`internal/game/`)

- `CollectibleField` — pure-logic, no goroutine/lock/DB. 600m grid spatial index,
  monotonic process-local IDs, per-region target populations.
- `CollectibleRegion`, `Collectible` exported types. Deterministic `timeNow`/`rng`
  seams for tests.
- Three fixed 200m-radius circular regions in Shanghai, 20 collectibles each.
  `config/collectible-regions.json`, `-collectible-regions` flag. Validates count,
  coordinates, radius, respawn bounds, overlap at startup.
- Pickup removes immediately; replacement scheduled after random 5-15s delay.
  Placement retries bounded (accepts last valid in-region position if all too close).

### Score persistence (`internal/storage/`)

- Migration `002_collectibles.sql`: `collectible_score BIGINT DEFAULT 0`,
  `is_synthetic BOOLEAN DEFAULT FALSE`.
- `ScorePersister` — single-goroutine worker, pending-map coalescing (highest
  score wins), exponential backoff retry (cap 30s). Async `Submit` for pickups,
  sync `SubmitSync`/`Drain` for disconnect/shutdown.
- `SaveScore`: `GREATEST` (MySQL) / `MAX` (SQLite) monotonic idempotent update.
  `RowsAffected == 0` is success.

### Synthetic identity

- `is_synthetic` is a persistent, server-trusted DB column. Queried by marker
  (not username prefix). `CorrectSyntheticMarkers` fixes pre-migration accounts.
- `auth.User` carries `IsSynthetic` → WebSocket → Hub. Client cannot control.
- Synthetic players move and are visible, but cannot collect items or appear in
  leaderboard rankings.

### Protocol (frozen before Hub changes)

- C→S: `collect` (`{"type":"collect","collectibleId":123}`)
- S→C init: `self_state` extended with `score`; `collectible_regions` (public
  geometry only); `visible_collectibles_snapshot`. Always 4 init messages.
- S→C replication: `collectiblesEntered`, `collectibleIdsLeft`,
  `collectiblesSpawned`, `collectibleIdsCollected` in `replication_update`.
- S→C winner: `collect_result` (`{"type":"collect_result","collectibleId":123,"score":42}`)
- Normalization: left > entered, collected > spawned (prevents contradictions).

### Hub integration (`internal/realtime/`)

- Collectible visibility tracked per-client with 500/600m hysteresis.
- `recalcCollectibleVisibility` only for moved players (not all-player scan).
- `advanceCollectibleReplacements` from Hub tick; reverse fan-out via
  `AOIIndex.QueryPlayerIDsNearPoint` (9-cell scan, not all players).
- `processCollect` in Hub actor (serial): validates connection, synthetic reject,
  300ms cooldown, existence, visibility, ≤10m distance. No DB call in path.
- Score incremented in-memory; persistence is async. Sync submit + drain on
  disconnect/logout/shutdown.

### Leaderboard

- `GET /api/leaderboard/online` — session auth, GET only.
- Hub actor builds ranking on demand: iterates online clients, filters synthetic,
  sorts by score desc + playerID asc. Returns Top 5 + self (rank + score).
- `self` omitted when requester is not connected. No cache/poll/push.

### Frontend (`web/`)

- Three translucent gold dashed region circles (Leaflet, non-interactive).
- Gold glowing gem markers (`divIcon`, radial gradient + box-shadow).
- Nearest-target selection (haversine ≤10m), highlighted with larger glow.
- `J` key and circular touch button share 300ms client cooldown.
- `collect_result` triggers `+1` pop animation and score display update.
- Leaderboard panel: hidden by default, fetches once on open, Top 5 + self rank.
- Full reset on logout/reconnect (regions, gems, score, leaderboard).

### Scale regression

- `collectible_scale_test.go` — 200-player deterministic scenario, 3 regions,
  60 collectibles. Proves AOI metrics identical with/without collectibles
  (no O(P×C) scan).

### What was NOT implemented

- Persistent collectible instances across restarts (scores persist, items reset).
- Inventory, item abilities, rounds, or score decay.
- Global leaderboard with offline users. Auto-refresh or push updates.
- Runtime region editing or config reload. Multi-process coordination.
- Zero-loss score durability (process kill may lose unpersisted points).

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
