# Map Walker Handoff

## Implementation Status

- Go module initialized (`map-walker`, Go 1.26).
- Backend state, message types, Hub, Client, and HTTP server implemented.
- WebSocket library: `github.com/coder/websocket`.
- Hub fixes: slow clients are fully disconnected (context cancel + socket close); duplicate `playerId` reconnects replace the old connection without removing the new one.
- Connection reliability: protocol-level heartbeat per client, unified connection lifecycle, frontend auto-reconnect with capped exponential backoff, and Chinese connection status.
- Browser frontend implemented with locally served Leaflet, Gaode map tiles, keyboard controls, mobile direction pad, and centered auth card.
- Verification command: `go test ./...`.
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

- Unauthenticated visitors see a centered login/register card over the map.
- Login and registration toggle within one card without page navigation.
- On success the card hides, an upper-right account control shows the username,
  and the WebSocket connects.
- Page refresh restores the session from the cookie.
- After max reconnect retries with persistent failure, the session is re-checked;
  if expired the auth card reappears.

## Project Layout

```text
cmd/map-walker/main.go       — entrypoint, graceful shutdown
internal/game/               — in-memory player positions, Step() returns moved IDs
internal/realtime/           — Hub actor, WebSocket client, message types, persistence interface
internal/server/             — HTTP routes, auth endpoints, WebSocket upgrade, static files
internal/auth/               — user registration/login, bcrypt, session tokens
internal/storage/            — SQLite/MySQL, forward-only migrations, user/session/position persistence
internal/storage/migrations/ — numbered SQL migration files
web/                         — Leaflet UI, auth card, app.js, styles, local assets
```

## Database

- Default: SQLite at `data/map-walker.db`. MySQL supported via `-db-driver mysql`.
- Migrations in `internal/storage/migrations/` are forward-only, applied in
  numbered order within transactions.
- Tables: `users`, `sessions`, `schema_migrations`.
- `users.last_lat` and `users.last_lng` store the last known position for
  restoration on reconnect.

## Known Limitations

- No AOI (Area of Interest), spatial indexing, or distance-based filtering —
  all players are in a single flat world visible to everyone. AOI is the next
  independent phase.
- No password recovery, email verification, or OAuth.
- Session expiry (30 days) does not proactively disconnect active WebSockets;
  the next reconnect must re-authenticate.
- Graceful shutdown timeout is 10 seconds; final position saves may be
  incomplete if that deadline is exceeded.
- Map tiles depend on Gaode (`webrd0*.is.autonavi.com`); availability follows
  that provider.
- Leaflet marker images are served from `web/images/`.
- No server-side input queue — only the latest input state is applied per tick.

## Authoritative Movement Phase

- Clients send directional input with a monotonically increasing sequence.
- `game.World` owns spawn positions, movement speed, coordinates, ticks, and
  dirty/removal tracking.
- Simulation runs at 20 Hz.
- Incremental broadcasts run at up to 10 Hz and skip empty deltas.
- New clients receive `world_snapshot`; existing clients receive
  `players_delta`.
- Frontend movement follows server output and sends neutral input on release,
  blur, and page hide.
- Verification: `go test ./...`, `go vet ./...`, and two-window browser testing.

## Connection Reliability Phase

- Each `realtime.Client` owns protocol-level ping/pong heartbeat and ends its
  lifecycle on heartbeat, read, or write failure.
- The Hub actor loop remains the only owner of connection and player removal.
- Duplicate-ID replacement safety is preserved: an obsolete connection cannot
  unregister its replacement.
- The browser keeps one current WebSocket and one retry timer, reconnects
  indefinitely with 1/2/4/8/10 second delays, and ignores events from obsolete
  sockets.
- Disconnected markers remain visible until the next `world_snapshot`.
- Verification: `go test ./internal/realtime`, manual stop/start server testing.
