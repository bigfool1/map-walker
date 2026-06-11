# Map Walker Handoff

## Implementation Status

- Go module initialized (`map-walker`, Go 1.26).
- Backend state, message types, Hub, Client, and HTTP server implemented.
- WebSocket library: `github.com/coder/websocket`.
- Hub fixes: slow clients are fully disconnected (context cancel + socket close); duplicate `playerId` reconnects replace the old connection without removing the new one.
- Connection reliability: protocol-level heartbeat per client, unified connection lifecycle, frontend auto-reconnect with capped exponential backoff, and Chinese connection status.
- Browser frontend implemented with locally served Leaflet, Gaode map tiles, keyboard controls, and mobile direction pad.
- Verification command: `go test ./...`.
- Manual verification target: `http://localhost:8080` (`go run ./cmd/map-walker`).

## Project Layout

```text
cmd/map-walker/main.go       — entrypoint, starts Hub and HTTP server on :8080
internal/game/               — in-memory player positions
internal/realtime/           — Hub actor, WebSocket client, message types
internal/server/             — /, /healthz, /ws routes
web/                         — Leaflet UI, app.js, styles, local assets
```

## Known Limitations

- No login, persistence, or player accounts.
- Reconnecting reuses `playerId` but does not restore the previous position or queued inputs; the server spawns the player again.
- No server-side session grace period or seamless state restoration across disconnects.
- Map tiles depend on Gaode (`webrd0*.is.autonavi.com`); availability follows that provider.
- Leaflet marker images are served from `web/images/`.
- Duplicate browser tabs can share the same `sessionStorage` player ID; the server now replaces the older connection, but separate tabs still need distinct IDs for true multi-player testing in one profile.

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
