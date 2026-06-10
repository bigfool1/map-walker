# Map Walker MVP Design

Date: 2026-06-10

## Goal

Build a small Go + browser demo for learning long-lived game backend connections.
The demo should show a real map, one local player marker, and multiplayer position
synchronization across multiple browser windows.

The project should be easy for a Python backend engineer to read. Explanatory
comments and Python comparisons should appear in the code around the important
learning points: Go channels, goroutines, WebSocket read/write loops, Hub
ownership, and frontend input normalization. Routine code should stay lightly
commented so the project still reads like normal Go and JavaScript.

## MVP Behavior

- The Go server serves a static browser frontend and a WebSocket endpoint.
- Each browser tab receives a generated player ID.
- The frontend shows a Leaflet map with OpenStreetMap tiles.
- The player can move with keyboard controls on desktop.
- The player can move with lightweight on-screen controls on mobile.
- The client sends position updates over WebSocket.
- The server stores the latest position for each connected player.
- The server broadcasts player snapshots to all connected clients.
- Opening two browser windows should show both players moving.

## Non-Goals

- No login or account system.
- No database or persistence.
- No creature catching, inventory, quests, or economy.
- No road snapping, collision, or real walkability rules.
- No frontend framework for the first version.
- No complex mobile gesture system.

## Architecture

```text
cmd/map-walker/main.go
  starts HTTP server and wires dependencies

internal/server/server.go
  owns routes: static files, health check, WebSocket upgrade

internal/realtime/hub.go
  owns connected clients, player state updates, and broadcasts

internal/realtime/client.go
  owns one WebSocket connection, with separate read and write loops

internal/realtime/messages.go
  defines client/server WebSocket message types

internal/game/state.go
  stores player positions and snapshot helpers

web/index.html
web/app.js
web/styles.css
  thin Leaflet frontend and input controls
```

The Hub is the central learning object. It is similar to a long-running
`asyncio` task that owns an `asyncio.Queue`, but in Go it will receive events
through typed channels. Each WebSocket client gets its own read loop and write
loop. The write loop consumes from a per-client send channel, which makes slow
client handling explicit.

The implementation should include comments at those boundaries. For example,
the Hub can explain how a channel-backed event loop compares to an
`asyncio.Queue` consumer, and the client write loop can explain why outbound
WebSocket writes are serialized through one goroutine.

## Data Flow

1. Browser loads the static page.
2. Frontend generates or receives a player ID and opens `/ws`.
3. Server registers the connection with the Hub.
4. Desktop keyboard input or mobile virtual buttons update the local intended
   position.
5. Frontend sends a JSON position message:

```json
{
  "type": "position_update",
  "playerId": "p123",
  "lat": 31.2304,
  "lng": 121.4737
}
```

6. The client read loop decodes the message and sends it to the Hub.
7. The Hub updates in-memory state.
8. The Hub broadcasts a snapshot message:

```json
{
  "type": "players_snapshot",
  "players": [
    { "id": "p123", "lat": 31.2304, "lng": 121.4737 }
  ]
}
```

9. Each frontend renders markers from the latest snapshot.

## Mobile Controls

Mobile support should be a thin frontend input adapter, not a separate backend
feature. The first version should add a small fixed-position directional pad:

- Up, down, left, right buttons.
- Buttons use pointer events so they work for touch and mouse.
- Holding a button repeatedly moves the local player.
- The same movement function is used by keyboard and mobile buttons.
- The WebSocket protocol stays unchanged.

This is intentionally light. It teaches the useful backend lesson: game servers
should receive normalized player intent or state updates, not care whether input
came from a keyboard, touch UI, controller, or automated test.

## Error Handling And Lifecycle

- On connect, register the client and send the latest snapshot.
- On read error or browser close, unregister the client and remove the player.
- If a client's send channel fills, treat it as a slow client and disconnect it.
- Add WebSocket ping/pong or read deadlines after the basic demo works.
- Frontend should show a small connection status indicator.
- Frontend reconnect can be added after the first working multiplayer loop.

## Testing Strategy

Initial tests should focus on backend logic that does not require a browser:

- State adds, updates, removes, and snapshots players correctly.
- Hub registers and unregisters clients.
- Hub broadcasts position updates to connected clients.
- Message encoding and decoding stays stable.

Manual verification is still important for the first UI:

- Start the server.
- Open two browser windows.
- Move one player with keyboard.
- Move another with mobile-style on-screen controls or a narrow viewport.
- Confirm both windows show the same player positions.

## Implementation Order

1. Create Go module and project folders.
2. Implement `game.State` with tests.
3. Implement realtime message structs.
4. Implement Hub registration, unregistration, and broadcast behavior.
5. Implement WebSocket client read/write loops.
6. Serve static frontend.
7. Build Leaflet map and player markers.
8. Add keyboard movement.
9. Add mobile virtual direction pad.
10. Add basic connection status and manual browser verification.
