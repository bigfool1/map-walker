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
