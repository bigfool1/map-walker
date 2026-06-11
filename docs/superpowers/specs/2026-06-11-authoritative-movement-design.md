# Authoritative Movement Design

Date: 2026-06-11

## Goal

Evolve Map Walker from client-authoritative position sharing into a small
authoritative game server.

Clients send movement intent rather than coordinates. The server advances the
world at a fixed simulation rate, owns all player positions, and broadcasts
batched incremental state changes at a lower frequency.

This phase should teach:

- Fixed-timestep game simulation.
- Separation between network input and game state.
- Server-authoritative movement.
- Input sequencing and stale-input rejection.
- Dirty-state tracking and incremental broadcast.
- Different simulation and network update frequencies.

## Scope

This phase includes:

- A server-owned `game.World`.
- Fixed simulation ticks at 20 Hz.
- Batched delta broadcasts at 10 Hz.
- Input-state messages with monotonically increasing sequence numbers.
- Server-owned spawn position and movement speed.
- Normalized diagonal movement.
- Full snapshots for newly connected clients.
- Incremental updates and removals for existing clients.
- Frontend keyboard and virtual direction controls that send input state.

This phase excludes:

- Area of interest or spatial partitioning.
- Collision, roads, and walkability.
- Client-side prediction.
- Rendering interpolation.
- Lag compensation or rollback.
- Persistence and accounts.
- Multiple backend nodes.

AOI is intentionally deferred. This phase reduces unnecessary broadcasts by
decoupling input, simulation, and network output and by sending only changed
players. Spatial filtering should be studied separately after this data flow is
working and measurable.

## Authority Boundary

The client may send only:

- Current directional input state.
- A monotonically increasing input sequence number.

The client must not decide:

- Position.
- Movement speed.
- Simulation tick.
- Spawn position.

Any coordinate-like fields included in an input JSON object are ignored because
the input message type does not decode or use them.

The server owns:

- Player creation and removal.
- Current player position.
- Latest accepted input state.
- Latest accepted input sequence.
- Movement speed.
- World tick.
- Dirty-player and removed-player tracking.

## Architecture

```text
Browser
  keyboard / direction pad
    -> normalized InputState
    -> WebSocket input message

realtime.Client
  readLoop
    -> decode input
    -> Hub.ApplyInput

realtime.Hub
  owns connections and outbound delivery
  routes connection and input events into World
  drives simulation and broadcast schedules

game.World
  owns players, input states, tick, and dirty tracking
  ApplyInput(playerID, input)
  AddPlayer(playerID)
  RemovePlayer(playerID)
  Step(deltaTime)
  Snapshot()
  TakeDelta()
```

`game.World` contains no WebSocket code and should be testable without
goroutines, timers, or real time.

`realtime.Hub` remains the actor-style event loop. It is the only goroutine that
mutates the World. This preserves the first phase's no-lock ownership model.

Python comparison:

- `Hub.Run` resembles one long-running `asyncio` task consuming connection and
  input events.
- `World.Step` resembles a pure domain/service method called by a periodic game
  loop.
- Dirty tracking resembles collecting changed ORM/domain objects for a batched
  flush, except the flush target is connected clients instead of a database.

## World Model

Suggested types:

```go
type InputState struct {
	Sequence uint64
	Up       bool
	Down     bool
	Left     bool
	Right    bool
}

type Player struct {
	ID            string
	Lat           float64
	Lng           float64
	Input         InputState
	LastSequence  uint64
}

type World struct {
	players          map[string]*Player
	tick             uint64
	dirtyPlayerIDs   map[string]struct{}
	removedPlayerIDs map[string]struct{}
}
```

The exact field placement may vary during implementation, but the World must
remain the owner of player state, accepted input sequence, and delta tracking.

## Movement Rules

Initial server parameters:

- Simulation frequency: 20 Hz.
- Fixed simulation step: 50 milliseconds.
- Broadcast frequency: 10 Hz.
- Broadcast every two simulation ticks.
- Movement speed: approximately 12 meters per second.

Movement is computed from the latest accepted input state:

```text
x = right - left
y = up - down
```

Rules:

- Opposite directions on the same axis cancel each other.
- If both axes are non-zero, normalize the vector so diagonal movement is not
  faster than straight movement.
- Convert the movement distance into latitude/longitude deltas using a small-map
  approximation suitable for this demo.
- A player with no effective movement is not marked dirty.
- Movement depends on server step duration and speed, not client message rate.

The approximation should be isolated behind a small movement function so it can
later be replaced without changing WebSocket or Hub code.

## Input Protocol

The old `position_update` message is replaced. It should not remain as an active
movement path.

Client to server:

```json
{
  "type": "input",
  "sequence": 42,
  "up": true,
  "down": false,
  "left": false,
  "right": true
}
```

The server derives the player ID from the connection. A client-provided
`playerId` is unnecessary and must not determine which player receives input.

Input handling:

- Accept input only when `sequence` is greater than the player's last accepted
  sequence.
- Ignore duplicate or stale sequence numbers.
- Store only the latest input state.
- Do not queue every input message for future simulation ticks.
- On key release, focus loss, page visibility loss, or pointer cancellation,
  the frontend sends a new input where all directions are false.

## Server Output Protocol

### Initial Snapshot

After registration, the new client receives a full world snapshot:

```json
{
  "type": "world_snapshot",
  "tick": 1280,
  "players": [
    {
      "id": "p1",
      "lat": 31.2304,
      "lng": 121.4737
    }
  ]
}
```

The snapshot establishes a complete rendering baseline.

### Incremental Delta

Existing clients receive batched deltas:

```json
{
  "type": "players_delta",
  "tick": 1282,
  "players": [
    {
      "id": "p1",
      "lat": 31.2305,
      "lng": 121.4738
    }
  ],
  "removedPlayerIds": ["p3"]
}
```

Rules:

- `players` contains only players changed since the previous broadcast.
- Multiple simulation ticks are merged into one latest-position update.
- `removedPlayerIds` reports players that genuinely leave the world.
- Empty deltas should not be broadcast.
- `TakeDelta` returns the current accumulated delta and clears dirty/removal
  tracking.
- A removed player must not also appear in `players` in the same delta.

The old `players_snapshot` broadcast-on-every-input behavior is removed.

## Runtime Data Flow

1. Client connects.
2. Hub registers the client.
3. World creates the player at the server-defined spawn position.
4. Hub sends that client a full `world_snapshot`.
5. World marks the new player dirty so existing clients learn about it in the
   next delta broadcast.
6. Client input changes and sends an `input` message with a new sequence.
7. Client read loop sends the decoded input event to Hub.
8. Hub applies the latest valid input to World.
9. Every 50 ms, Hub calls `World.Step(50ms)`.
10. World updates moving players and marks them dirty.
11. Every 100 ms, Hub calls `World.TakeDelta()`.
12. Hub broadcasts a `players_delta` only when it contains changes or removals.
13. On disconnect, Hub removes the player from World.
14. The next broadcast reports the player ID in `removedPlayerIds`.

## Scheduling

The first implementation should keep scheduling inside `Hub.Run`:

```text
select
  register event
  unregister event
  input event
  simulation ticker
  broadcast ticker
  stop event
```

Use two tickers:

- Simulation ticker: 50 ms.
- Broadcast ticker: 100 ms.

Although broadcasting could be expressed as every second simulation tick, two
explicit tickers make the two rates independently visible for learning and
future tuning.

Ticker creation should be injectable or configurable enough that World tests do
not use real time. Hub integration tests may use short deterministic durations,
but most simulation behavior belongs in direct World tests.

## Connection Lifecycle

Retain the first phase's lifecycle protections:

- A slow client's full send queue disconnects that client.
- Removing a client also closes/cancels its WebSocket lifecycle.
- Duplicate player IDs replace the old connection safely while keeping one
  world player with that ID.
- A stale old connection must not remove the newer connection.

Additional rules:

- Replacing a connection resets that player's input to neutral so an input held
  by the old connection cannot continue movement.
- Replacing a connection does not emit a removal for the same player ID.
- A genuine disconnect removes the player and its input state.
- A disconnected player cannot continue moving.
- Hub shutdown stops both tickers.
- Hub shutdown closes all clients.
- Public Hub methods must not block forever after shutdown.

Reconnect persistence after the old connection has fully left is out of scope.
A later reconnect creates a new player at the server-defined spawn position. If
the browser reconnects with the same player ID while the old connection is still
registered, Hub treats it as an immediate connection replacement and preserves
the current server-owned position.

## Frontend Input State

Keyboard and direction-pad controls share one local input object:

```javascript
const input = {
  up: false,
  down: false,
  left: false,
  right: false,
};
```

Frontend behavior:

- `keydown` changes a direction to `true`.
- `keyup` changes it to `false`.
- Pointer press changes a direction to `true`.
- Pointer release/cancel changes it to `false`.
- Send only when the effective input state changes.
- Increment `sequence` for every sent state.
- Ignore browser key-repeat as a new movement event.
- On `blur` and `visibilitychange` to hidden, clear all input and send the stop
  state.
- Local position changes only in response to server snapshot/delta messages.
- The local map may pan to the server-reported local player position.

The frontend no longer runs a movement interval. Holding a key or direction
button is represented by a persistent `true` input state on the server.

## Error Handling

- Malformed JSON is logged and ignored.
- Unknown message types are ignored.
- Input for a player no longer registered in World is ignored.
- Stale or duplicate input sequences are ignored.
- A failed snapshot or delta encode should be returned as an error from the
  encoding helper rather than panic where practical.
- A write failure ends that client's connection lifecycle.
- Tick processing should not block on network writes because clients receive
  encoded messages through bounded send channels.

## Testing Strategy

### World Unit Tests

- `AddPlayer` creates a player at the configured spawn.
- `ApplyInput` accepts a newer sequence.
- `ApplyInput` rejects duplicate and stale sequences.
- Equal simulated duration produces equal displacement regardless of client
  input message frequency.
- Straight movement matches configured speed.
- Diagonal movement has the same total speed as straight movement.
- Opposite directions cancel on that axis.
- No effective movement produces no dirty player.
- Several steps merge into one latest-position delta.
- `TakeDelta` clears accumulated dirty state.
- Removing a player adds it to removals.
- A removed player does not also appear as changed.

### Protocol Tests

- Input JSON decodes into the expected state and sequence.
- Snapshot JSON contains type, tick, and all players.
- Delta JSON contains type, tick, changed players, and removals.
- Extra coordinate fields in an input message do not affect movement.

### Hub Tests

- Register sends a full snapshot only to the new client.
- Register marks the new player for the next delta to existing clients.
- Simulation ticks update World without immediately broadcasting.
- Broadcast ticks send accumulated changes.
- Empty broadcast ticks send nothing.
- Disconnect is announced in the next delta.
- Slow clients are disconnected.
- Duplicate IDs do not allow stale unregister events to remove replacements.
- Immediate duplicate-ID replacement preserves one world player, resets input,
  and does not emit a removal.
- Hub shutdown stops tickers and client activity.

### Frontend Manual Verification

- Holding one key moves continuously without repeated input messages.
- Releasing the key stops movement.
- Losing browser focus stops movement.
- Keyboard and direction pad produce identical server behavior.
- Two browser windows see batched movement updates.
- The browser never sends latitude or longitude.
- Local movement follows server messages rather than changing immediately on
  input.

## Observability For Learning

Add lightweight counters or periodic logs sufficient to compare phase one and
phase two:

- Connected clients.
- Accepted input messages per second.
- Simulation ticks per second.
- Delta broadcasts per second.
- Changed players per delta.
- Encoded delta size in bytes.

This does not require a metrics server in this phase. A periodic structured log
line is enough. The goal is to make the performance effect observable before
adding AOI.

## Success Criteria

The phase is complete when:

- Clients cannot directly set player coordinates.
- Movement speed is determined by server ticks and constants.
- Holding input moves continuously; stopping input stops movement.
- Different input event frequencies do not change movement speed.
- Simulation runs at 20 Hz and network deltas are produced at no more than
  10 Hz.
- Existing clients receive only changed players and removals.
- New clients receive a complete snapshot.
- All World, protocol, and Hub tests pass.
- Two browser windows demonstrate authoritative synchronized movement.
- Logs make input, tick, delta frequency, player count, and delta size visible.

## Migration Notes

This phase intentionally replaces these phase-one concepts:

| Phase One | Phase Two |
|---|---|
| `game.State` | `game.World` |
| `position_update` | `input` |
| Client-provided coordinates | Server-computed coordinates |
| Update immediately mutates position | Fixed simulation tick mutates position |
| Broadcast after every update | Broadcast ticker |
| Full `players_snapshot` each update | Initial `world_snapshot` plus `players_delta` |
| Frontend movement interval | Persistent input state |

Temporary compatibility code is unnecessary because this is a demo with one
frontend and one backend version developed together.
