# Connection Reliability Design

Date: 2026-06-11

## Goal

Add a small connection-reliability layer to Map Walker so stale WebSocket
connections are detected and browsers recover automatically after temporary
server or network failures.

This phase should preserve the existing server-authoritative movement model and
Hub actor ownership.

## Scope

This phase includes:

- Protocol-level WebSocket heartbeat from the server.
- Cleanup of connections that stop responding.
- Continuous frontend reconnection with exponential backoff.
- Clear connection and retry status in the existing status indicator.
- Reuse of the tab's existing `playerId` after reconnecting.
- A full world snapshot after reconnection to correct the retained map view.

This phase excludes:

- Restoring the player's position from before the disconnection.
- Server-side session retention or reconnect grace periods.
- Queuing input history while disconnected.
- User controls for stopping or manually triggering reconnection.
- Application-level JSON heartbeat messages.
- New configuration surfaces for heartbeat or retry timing.

## Connection Semantics

The browser keeps its `playerId` in `sessionStorage` and uses that ID for every
connection attempt made by the tab.

When a connection is lost, the server removes the player through the existing
Hub unregister path. A later connection with the same ID is a new player
registration and uses the server-defined spawn position.

The reconnecting browser keeps the old map markers visible. They are understood
to be stale while the status indicator reports that the connection is being
restored. The `world_snapshot` sent after registration becomes the new rendering
baseline and removes or updates stale markers.

## Server Lifecycle

Each `realtime.Client` owns the liveness checks for its WebSocket connection.
It periodically uses protocol-level Ping/Pong handling to determine whether the
peer is still responsive.

A heartbeat failure, read failure, or write failure ends the same client
lifecycle. The existing deferred unregister operation then submits the
disconnection to `Hub.Run`, which remains the only owner of connection and
world-state mutation.

The Hub does not schedule heartbeats and does not gain locks. Its existing
duplicate-ID protection remains responsible for ensuring that an old
connection cannot unregister a newer replacement.

Heartbeat timing remains an internal realtime-package policy rather than a
command-line or application configuration option.

## Frontend Lifecycle

The frontend manages one active connection attempt at a time.

Initial load:

- Show `连接中`.
- Open the WebSocket using the tab's existing `playerId`.

Successful connection:

- Show `已连接`.
- Reset the retry counter and backoff.
- Send only the latest input state.
- Apply the incoming full snapshot as the authoritative map state.

Connection loss:

- Keep the current map markers.
- Show `连接已断开，正在重连（第 N 次）`.
- Schedule exactly one reconnect attempt.
- Increase the delay through 1, 2, 4, and 8 seconds, then retry every 10
  seconds until the page is closed.

The WebSocket `close` event is the single trigger for scheduling a reconnect.
The `error` event may update no lifecycle state independently, because browsers
normally follow it with `close` and a second trigger could create parallel
attempts.

Events from an obsolete WebSocket must not replace status, messages, or retry
state belonging to a newer connection.

## Input Behavior

The frontend does not queue inputs or replay input history while disconnected.
Keyboard and joystick state may continue to change locally.

After a connection opens, the client sends its current input state with the next
monotonically increasing sequence number. The new server-side player therefore
starts from the configured spawn position and immediately reflects the user's
current controls.

## User Interface

The existing status element supports three user-facing states:

- `连接中`
- `已连接`
- `连接已断开，正在重连（第 N 次）`

No retry button, stop button, modal, or additional connection panel is added.

## Failure Handling

- Heartbeat, read, and write failures all terminate the affected connection.
- A failed client is removed through the Hub actor loop.
- A full outbound client queue continues to disconnect that slow client.
- A failed frontend attempt waits for `close` before scheduling its successor.
- At most one retry timer and one current WebSocket may control the lifecycle.
- An obsolete connection cannot remove or override its replacement.

## Testing

Automated Go tests should cover the server behavior added around connection
liveness and preserve the existing duplicate-ID replacement guarantees.

Frontend retry behavior should be tested without introducing a new JavaScript
toolchain solely for this phase. If the retry calculation and lifecycle can be
isolated and tested with the project's existing tools at low cost, cover:

- Exponential delay growth.
- The 10-second retry ceiling.
- Retry reset after a successful connection.
- Prevention of duplicate retry scheduling.
- Ignoring events from obsolete connections.

Otherwise, verify those behaviors manually in the browser.

Project verification:

```bash
go test ./...
go vet ./...
```

Manual verification:

1. Open the application and confirm it reaches `已连接`.
2. Stop the server and confirm the map remains visible while retry attempts
   continue with increasing delays.
3. Restart the server and confirm the tab reconnects without a refresh.
4. Confirm the same `playerId` is used but the player appears at the server
   spawn position.
5. Confirm the new full snapshot corrects retained stale markers.
6. Repeat with two windows and confirm an obsolete connection cannot remove a
   newer connection using the same ID.

## Planning Constraint

The implementation plan for this phase must stay at task level. It should state
behavioral goals, module boundaries, affected areas, and verification criteria,
but must not include implementation code, complete function bodies, or a
line-by-line implementation recipe.
