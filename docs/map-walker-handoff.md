# Map Walker Handoff

## Implementation Status

- Go module initialized (`map-walker`, Go 1.26).
- Backend state, message types, Hub, Client, and HTTP server implemented.
- WebSocket library: `github.com/coder/websocket`.
- Hub fixes: slow clients are fully disconnected (context cancel + socket close); duplicate `playerId` reconnects replace the old connection without removing the new one.
- Connection reliability: protocol-level heartbeat per client, unified connection lifecycle, frontend auto-reconnect with capped exponential backoff, and Chinese connection status.
- Browser frontend implemented with Leaflet (CDN), Gaode map tiles, keyboard controls, mobile direction pad, centered auth card, account menu, and appearance editor.
- Player appearance sync complete: persistent marker color/shape, CSS `divIcon` markers, `PUT /api/appearance`, and realtime appearance replication via `replication_update`.
- Verification command: `go test ./...` and `go vet ./...`.
- Manual verification target: `http://localhost:8080` (`go run ./cmd/map-walker`).

## Account and Position Persistence Phase (Complete)

### User accounts

- `auth.Service` handles registration, login, logout, and session authentication.
- Passwords hashed with bcrypt; session tokens are random base64 strings,
  SHA-256 hashed before storage.
- `map_walker_session` cookie: HttpOnly, SameSite=Lax, 30-day expiry.
- WebSocket identity is authenticated from the session cookie; client-supplied
  player IDs are ignored.

### Position persistence

- `PersistenceWorker` runs a single background goroutine that writes position
  updates to SQLite/MySQL in order, rejecting stale updates by per-user sequence
  number.
- `World.Step()` returns moved player IDs so the Hub can track dirty players.
- Every 5 seconds, only players moved since the last interval are submitted
  asynchronously — simulation (20 Hz) and broadcasts (10 Hz) are never blocked.
- Genuine disconnects trigger a synchronous final position save.
- Same-account connection replacement (page refresh, multi-tab) does not trigger
  a final save — the in-memory position is retained.

### Logout and graceful shutdown

- Logout disconnects the WebSocket, saves the final position synchronously, then
  revokes the session and clears the cookie — in that order.
- SIGINT/SIGTERM triggers graceful shutdown: stop accepting connections, save
  all online positions, drain the persistence worker, close WebSockets, stop the
  Hub, close the database.
- Frontend does not auto-reconnect after intentional logout.

### Authentication UI

- Unauthenticated visitors see a centered login/register card over the map;
  the joystick is hidden and WASD/arrow keys are not captured, allowing normal
  typing in the form fields.
- Login and registration toggle within one card without page navigation.
- On success the card hides, the joystick reappears, an upper-right account
  control shows the username, and the WebSocket connects.
- Page refresh restores the session from the cookie.
- After max reconnect retries with persistent failure, the session is re-checked;
  if expired the auth card reappears.
- Logout resets the card to login mode regardless of which mode was last used.
- Intentional logout prevents auto-reconnect.

## Player Appearance Sync Phase (Complete)

Design and plan: `docs/superpowers/specs/2026-06-12-player-appearance-sync-design.md`,
`docs/superpowers/plans/2026-06-12-player-appearance-sync.md`.

### Storage and auth

- Migration `002_appearance.sql` adds non-null `users.appearance_color` and
  `users.appearance_shape` with defaults `#3388ff` / `circle`.
- `auth.Service` and session responses carry stored appearance.
- `PUT /api/appearance` validates `#RRGGBB` colors and four shapes, normalizes
  color to lowercase, persists first, then waits for Hub application.
- Hub unavailable after a successful save returns `503`; the database value is
  not rolled back.

### World state and protocol

- `game.PlayerState` in `self_state` includes `id`, `username`, `lat`,
  `lng`, and nested `appearance`.
- `visible_entities_snapshot` carries AOI-filtered neighbor state on connect.
- `replication_update` batches `entered`, `left`, `positions`, `appearances`,
  and optional `selfPosition` per 100ms tick; empty updates are skipped.
- Saved appearance changes queue AOI-filtered appearance replication on the next
  broadcast tick.
- First connection loads persisted position and appearance; same-account
  replacement retains in-memory position, appearance, and username.

### Frontend

- Markers use `L.divIcon` with CSS shapes (`circle`, `square`, `diamond`,
  `triangle`) and authoritative color.
- `self_state` and `visible_entities_snapshot` create or refresh marker state;
  `replication_update` applies entered/left/position/appearance deltas.
- Hover tooltips show `You` for the current user and other players' usernames.
- Upper-right account trigger opens a menu with appearance editing and logout.
- Editor supports local preview, shape selection, native color input, save,
  cancel, and error display; edits are not sent until save.
- Registration, login, session bootstrap, snapshot, and appearance broadcasts
  initialize or refresh the authoritative appearance shown in the account UI.

### Verification

- Automated: `go test ./...`, `go vet ./...`.
- Manual: two-window appearance sync, movement after appearance changes,
  refresh/reconnect restoration, account menu interaction, save/cancel/retry.

## Project Layout

```text
cmd/map-walker/main.go       — entrypoint, graceful shutdown
internal/game/               — player positions, appearance, Step() returns moved IDs
internal/realtime/           — Hub actor, WebSocket client, message types, persistence interface
internal/server/             — HTTP routes, auth/appearance endpoints, WebSocket upgrade, static files
internal/auth/               — user registration/login, bcrypt, session tokens, appearance validation
internal/storage/            — SQLite/MySQL, forward-only migrations, user/session/position/appearance persistence
internal/storage/migrations/ — numbered SQL migration files (`001` initial, `002` appearance)
web/                         — Leaflet UI, auth card, account menu, appearance editor, app.js, styles
```

## Database

- Default: SQLite at `data/map-walker.db`. MySQL supported via `-db-driver mysql`.
- Migrations in `internal/storage/migrations/` are forward-only, applied in
  numbered order within transactions.
- Tables: `users`, `sessions`, `schema_migrations`.
- `users.last_lat` and `users.last_lng` store the last known position for
  restoration on reconnect.
- `users.appearance_color` and `users.appearance_shape` store the last saved
  marker appearance.

## Known Limitations

- AOI covers authenticated online players in the single World only; no
  synthetic entities, multi-Hub sharding, or geographic Shard boundaries yet.
- No password recovery, email verification, or OAuth.
- Session expiry (30 days) does not proactively disconnect active WebSockets;
  the next reconnect must re-authenticate.
- Graceful shutdown timeout is 10 seconds; final position saves may be
  incomplete if that deadline is exceeded.
- Map tiles depend on Gaode (`webrd0*.is.autonavi.com`); availability follows
  that provider.
- Leaflet is loaded from the unpkg CDN in `web/index.html`.
- No server-side input queue — only the latest input state is applied per tick.
- If the Hub is unavailable after a successful appearance save, online World
  state may lag the database until the user reconnects.
- MySQL periodic position persistence uses chunked bulk updates; SQLite remains
  per-row. SubmitSync and final-save lifecycle semantics are unchanged in this
  phase.

## Authoritative Movement Phase

- Clients send directional input with a monotonically increasing sequence.
- `game.World` owns spawn positions, movement speed, coordinates, ticks, and
  dirty/removal tracking.
- Simulation runs at 20 Hz.
- Incremental replication runs at up to 10 Hz and skips empty updates.
- New clients receive `self_state` and `visible_entities_snapshot`; ongoing
  changes use `replication_update`.
- Frontend movement follows server output and sends neutral input on release,
  blur, and page hide.
- Verification: `go test ./...`, `go vet ./...`, and two-window browser testing.

## Online Player AOI Phase (Complete)

Design and plan: `docs/superpowers/specs/2026-06-13-online-player-aoi-design.md`,
`docs/superpowers/plans/2026-06-13-online-player-aoi.md`.

### Spatial index

- `game.AOIIndex` uses a 600m square Cell grid with Shanghai-local meter
  coordinates.
- Enter radius 500m, leave radius 600m (hysteresis).
- Symmetric visibility relationships; recalculation runs only for players that
  moved since the previous replication tick.

### Hub replication

- Connect/disconnect updates AOI and queues entered/left for visible neighbors.
- Init snapshots are AOI-filtered (`visible_entities_snapshot`).
- One `replication_update` per changed client per 100ms tick.
- Invisible neighbors receive no position or appearance data.
- Interval stats log AOI candidate checks, distance checks, relationship
  changes, and replication payload bytes.

### Scale test

- `internal/realtime/aoi_scale_test.go` runs a deterministic 1,000-client
  in-memory scenario (sparse grid + dense local cluster) through movement,
  hysteresis, appearance, disconnect, and connection replacement.
- This is functional coverage, not a production capacity claim.

### Verification

- Automated: `go test ./internal/game ./internal/realtime`, `go test ./...`,
  `go vet ./...`.
- Manual: multi-browser AOI visibility (near/far players, movement entry/exit,
  appearance, reconnect/replacement).

## AOI Allocation Optimization Phase (Complete)

Design and plan: `docs/superpowers/specs/2026-06-14-aoi-allocation-optimization-design.md`,
`docs/superpowers/plans/2026-06-14-aoi-allocation-optimization.md`.

### Changes

- AOI movement-path collections (`Entered`, `Left`, `VisibleNeighbors`) are
  explicitly unordered; `EncodeVisibleEntitiesSnapshot` sorts by player ID for
  deterministic wire output.
- `recalculateRelationships` traverses nine-cell maps directly, returns
  discovery-order changes, and splits leave detection from removal.
- Removed `nineCellCandidates`, `sortedCopy`, and movement-path sorting
  allocations.

### A1 benchmark (100k / 10k / normal, seed 42, Mac M5)

Compared against baseline commit `3af14009` at optimized commit `d174e9a`:

- Core tick median: 274ms → 102ms (−63%)
- Δ heap allocation per run: 36.9 GB → 14.4 GB (−61%)
- AOI diagnostic counters unchanged (candidate pairs, distance checks,
  relationships entered/left)

Full comparison: `docs/benchmarks/aoi-core-baseline.md` Section 9.  
Artifacts: `docs/benchmarks/profiles/100k-10k-normal-core-a1-repeat{1,2,3}.json`.

### Verification

- `go test ./...`
- `go vet ./...`
- A1 core_tick benchmark (3 process-isolated repeats)

## Connection Reliability Phase

- Each `realtime.Client` owns protocol-level ping/pong heartbeat and ends its
  lifecycle on heartbeat, read, or write failure.
- The Hub actor loop remains the only owner of connection and player removal.
- Duplicate-ID replacement safety is preserved: an obsolete connection cannot
  unregister its replacement.
- The browser keeps one current WebSocket and one retry timer, reconnects
  indefinitely with 1/2/4/8/10 second delays, and ignores events from obsolete
  sockets.
- Disconnected markers remain visible until the next `visible_entities_snapshot`
  or `replication_update` left event.
- Verification: `go test ./internal/realtime`, manual stop/start server testing.

## Synthetic Clients Phase (Complete)

All 12 tasks of the Synthetic Clients phase are complete.  Verification:
`go test ./...` and `go vet ./...` pass.

### Overview

Synthetic clients are in-process WebSocket bots that connect to the Hub under
real accounts, send periodic movement inputs, and exercise AOI + replication
at scale without real users.  They are entirely opt-in: the server behaves
identically to the pre-phase version when `-synthetic-clients 0` (the default).

### Architecture

```
cmd/map-walker/main.go
  └─ buildSyntheticManager()          creates Manager if -synthetic-clients > 0
       └─ synthetic.Manager           single-goroutine actor, ramp-up scheduler
            ├─ synthetic.Provisioner  provisions bot accounts via HTTP/API
            └─ synthetic.Client       implements realtime.ClientSender, wraps
                                      github.com/coder/websocket
```

`Manager.Run()` is an actor loop (mirrors Hub.Run()) that owns client
lifecycle, behavior scheduling, and stats aggregation.  No locking outside the
loop.

### New packages and files

| File | Purpose |
|------|---------|
| `internal/synthetic/client.go` | WebSocket bot client, `realtime.ClientSender` impl |
| `internal/synthetic/manager.go` | Manager actor loop, ramp-up, behavior scheduling |
| `internal/synthetic/stats.go` | `SyntheticSnapshot` immutable stats struct |
| `internal/synthetic/provisioner.go` | Account provisioning via HTTP |
| `internal/realtime/stats.go` | `HubSnapshot` immutable stats struct |
| `internal/realtime/manual_hub.go` | Test helper: channel-driven tick control |
| `internal/server/admin.go` | Token-protected admin handlers |
| `web/admin.html` | Read-only operator dashboard |
| `web/admin.js` | 1 Hz polling, sessionStorage token, card renderer |
| `web/admin.css` | Dark monospace card layout |
| `cmd/map-walker/main_test.go` | Flag validation and lifecycle tests |

### Command-line flags

| Flag | Default | Description |
|------|---------|-------------|
| `-synthetic-clients N` | `0` | Bot count; `0` disables the Manager entirely |
| `-synthetic-ramp-rate N` | `5` | Connections per second during ramp-up |
| `-synthetic-auto-provision` | `false` | Register bot accounts on first run |

### Environment variables

| Variable | Description |
|----------|-------------|
| `MAP_WALKER_ADMIN_TOKEN` | Enables `/admin` and `/api/admin/synthetic-stats`; unset → 404 |
| `MAP_WALKER_ADMIN_PASSWORD` | Required when `-synthetic-auto-provision` is set |

### Shutdown order

1. `httpServer.Shutdown()` — stop accepting new connections
2. `manager.Stop()` — drain synthetic clients (if Manager is running)
3. `hub.Stop()` — final position save, close real clients
4. `db.Close()`

### Metrics exposed by `/api/admin/synthetic-stats`

**Hub (all clients):**
ConnectedClients, SimulationTicks, MovedPlayers, AOICandidatePairs,
AOIDistanceChecks, RelationshipsEntered, RelationshipsLeft,
ReplicationMessages, ReplicationRecipients, ReplicationBytes, SampledAt

**Synthetic (if Manager is running):**
TargetCount, ActiveCount, ActivatingCount, MovingCount, IdleCount,
FailedCount, InputsPerSecond, MessagesPerSecond, BytesPerSecond,
TotalActivated, TotalMessages, SampledAt

### Admin page

`/admin` serves `web/admin.html`.  The page:
- Stores the token only in `sessionStorage` (tab-scoped, never in cookies,
  localStorage, URL, or server markup).
- Polls `/api/admin/synthetic-stats` every 1 second with `Authorization: Bearer`.
- On 401 clears the token and shows the input form again.
- On 404 shows "Admin API not enabled on this server."
- Does **not** include charts, history, client lists, start/stop controls,
  or any write actions.

### What was NOT implemented

- HTTP/WebSocket bot (synthetic clients connect as in-process goroutines only)
- JWT authentication migration
- Dynamic client resizing via API
- Gameplay AI or pathfinding
- Multi-Hub federation

## Replication Reverse Fan-Out Phase (Complete)

Design and plan: `docs/superpowers/specs/2026-06-15-replication-reverse-fanout-design.md`,
`docs/superpowers/plans/2026-06-15-replication-reverse-fanout.md`.

### Problem

`broadcastReplication` used an O(clients × movers) nested loop: for each
connected client, iterate every moved player, check old and new visibility,
and build a per-client `ReplicationChanges`. At ~2,800 clients and ~2,000
movers per broadcast this produced millions of visibility lookups before JSON
encoding and send-queue work, pushing broadcast duration past the 100ms
interval and causing sim ticks to drop from 20 Hz to 8–10 Hz.

### Changes

- **Task 1 — regression tests:** 7 new Hub tests freeze multi-mover, same-tick
  multi-entry/leave, self-position suppression, queue-full removal, and movement
  directionality semantics. 20× repeat stable.
- **Task 2 — deterministic benchmarks:** 2,000- and 3,000-client in-memory
  benchmarks with the same deterministic placement hash as synthetic clients.
  Direct `Step` + `broadcastReplication` calls eliminate select-loop randomness;
  all logical counters (msgs, bytes, moved, entered, left) are identical across
  repeated runs.
- **Task 3 — mover-local visibility snapshots:** replaced `snapshotVisibility()`
  (copied every connected client's visible set) with `snapshotMoverVisibility()`
  (captures old neighbors only for moved players). Removed `wasVisibleTo`.
- **Task 4 — recipient accumulation fan-out:** replaced the client-by-mover scan
  with a broadcast-local `map[int64]*ReplicationChanges` accumulator. Self
  positions, stable positions, entered, left, and appearances fan outward from
  each changed player to its relevant recipients. Encode and send only for
  recipients that accumulated changes. `NormalizeReplicationChanges`,
  `TryEncodeReplicationUpdate`, stats counters, and queue-full removal remain
  unchanged.

### Data flow

```
1. Take moved player IDs from World
2. Snapshot mover-local old visibility (before AOI update)
3. Apply AOI movement updates (existing applyMovementAOIChanges)
4. Accumulate into map[recipientID]*ReplicationChanges:
   a. Self positions for connected movers
   b. Stable positions from movers → old+current neighbors
   c. Pending entered from each entrant → visible connected neighbors
   d. Pending left (already keyed by recipient)
   e. Pending appearances to changed player + visible connected neighbors
5. Encode + send only for accumulated, still-connected recipients
```

### Performance (Apple M5, go1.26.3)

| Scale | Baseline | Optimised | Speedup |
|-------|----------|-----------|---------|
| 2,000 clients | ~97.0 ms | ~15.3 ms | **6.3×** |
| 3,000 clients | ~211.7 ms | ~34.4 ms | **6.1×** |

Full comparison: `docs/benchmarks/replication-reverse-fanout.md`.
All logical counters (msgs, bytes, moved, entered, left) are identical to baseline.

### Remaining bottlenecks

- JSON encoding (2,000–3,000 messages, 3–7 MB per broadcast) dominates the
  remaining ~15–34 ms.
- Channel send/drain for thousands of clients adds overhead.
- Real-world Hub select loop adds goroutine scheduling and channel multiplexing
  overhead not captured by the synchronous benchmark.

These are evidence for subsequent encoding, transport, or persistence phases.

### Verification

- `go test ./...` — all tests pass
- `go test -race ./internal/realtime` — no races
- `go vet ./...` — clean
- Key replication tests 20× repeat — stable
- Benchmark logical counters — identical to baseline

## MySQL Position Batch Persistence Phase (Complete)

Design and plan: `docs/superpowers/specs/2026-06-15-mysql-position-batch-persistence-design.md`,
`docs/superpowers/plans/2026-06-15-mysql-position-batch-persistence.md`.

### Problem

Periodic position persistence used one `UPDATE users` per player, producing up
to 4,000 serial database calls per 5-second interval. On MySQL this meant
thousands of round trips competing with replication and tick work.

### Changes

- **Task 1 — sequence semantics:** 3 new `PersistenceWorker` tests freeze
  same-batch collapse (highest seq wins), failed-save lastSeq preservation, and
  cross-batch failure isolation. `saveRecorder` test helper with mutex-protected
  state for race-free concurrent observation.
- **Task 2 — MySQL bulk query:** `SavePositionChunk` in `position_batch.go`
  executes one parameterized `UPDATE users AS u JOIN (SELECT ? AS id, ... UNION
  ALL ...) AS positions ON positions.id = u.id SET u.last_lat = positions.lat,
  u.last_lng = positions.lng` per ≤500-row chunk in its own transaction.
  `RowsAffected` is not checked — chunk success is commit success.
- **Task 3 — backend routing:** `PersistenceWorker.apply()` splits into
  `filterAndCollapse()` (shared per-batch dedup and stale-seq filter) followed
  by `applyBulk()` (MySQL, ≤500-row chunks, sequential, failure-continue) or
  `applyPerRow()` (SQLite, existing per-row path). Worker goroutine count,
  Submit/SubmitSync/Drain/Stop signatures, and Hub code unchanged.
- **Task 4 — benchmarks:** 1,000- and 4,000-update MySQL benchmarks comparing
  per-row baseline against chunked bulk. Chunk counts verified (2 and 8).
- **Task 5 — documentation:** README, AGENTS, `.env.example`, and this handoff
  updated. MySQL documented as production target; SQLite as legacy/dev.

### Architecture

```
PersistenceWorker.apply(batch)
  └─ filterAndCollapse(batch)        // per-user dedup, stale-seq filter
       └─ accepted
            ├─ MySQL  → applyBulk()   // split ≤500, tx per chunk, fail-continue
            └─ SQLite → applyPerRow() // per-row SaveUserPosition, existing behavior
```

- `MaxPositionChunkSize = 500` (exported constant).
- Each chunk: BEGIN → `UPDATE ... JOIN` → COMMIT (or ROLLBACK on failure).
- Failed chunk does not advance `lastSeq` for any user in that chunk.
- Worker logs one error per failed chunk (with offset and size), not per row.

### Performance (Apple M5, MySQL 9.3.0, go1.26.3)

| Batch | Baseline (per-row) | Optimised (chunked) | Speedup | DB calls |
|-------|-------------------|---------------------|---------|----------|
| 1,000 | 160.4ms | 9.3ms | **17.3×** | 1,000 → 2 |
| 4,000 | 622.1ms | 38.8ms | **16.0×** | 4,000 → 8 |

Full comparison: `docs/benchmarks/mysql-position-batch-persistence.md`.

### What was NOT changed

- Five-second persistence interval.
- Hub dirty-player collection.
- `SubmitSync` behavior, disconnect, logout, replacement, or shutdown lifecycle.
- Final-save path (still uses `SubmitSync` → per-row or chunk depending on backend).
- SQLite dependencies or tests.

### Remaining limitations

- Final saves (disconnect, logout, shutdown) still use the persistence worker
  path; no micro-batch optimization for final saves.
- Missing-row detection is not implemented — `RowsAffected` is not checked in
  the chunked path.
- No parallelism — chunks are processed sequentially in the single worker
  goroutine.

### Verification

- `go test ./...` — all tests pass
- `go test -race ./internal/storage ./internal/realtime` — no races
- `go vet ./...` — clean
- Key persistence tests 20× repeat — stable
- Chunk counts verified: 1,000 → 2, 4,000 → 8
- Benchmark logical rows identical between baseline and optimised
