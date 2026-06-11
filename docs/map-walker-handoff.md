# Map Walker Handoff

## Implementation Status

- Go module initialized (`map-walker`, Go 1.26).
- Backend state, message types, Hub, Client, and HTTP server implemented.
- WebSocket library: `github.com/coder/websocket`.
- Hub fixes: slow clients are fully disconnected (context cancel + socket close); duplicate `playerId` reconnects replace the old connection without removing the new one.
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
- No WebSocket heartbeat, read deadlines, or frontend auto-reconnect.
- Map tiles depend on Gaode (`webrd0*.is.autonavi.com`); availability follows that provider.
- Leaflet marker images are served from `web/images/`.
- Duplicate browser tabs can share the same `sessionStorage` player ID; the server now replaces the older connection, but separate tabs still need distinct IDs for true multi-player testing in one profile.
