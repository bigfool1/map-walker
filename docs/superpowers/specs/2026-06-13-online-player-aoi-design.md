# Online Player AOI Design

Date: 2026-06-13

## Goal

Replace global player broadcasts with server-authoritative area-of-interest
(AOI) replication for online players.

Players should receive their own authoritative state separately from the public
state of nearby players. Nearby visibility is symmetric, uses a spatial grid
for candidate lookup, and applies exact distance checks with hysteresis.

This phase establishes correct single-Hub AOI behavior. A separate later phase
will add synthetic entities, million-entity benchmarks, Hub integration
benchmarks, and localhost WebSocket load generation.

## Scope

This phase includes:

- AOI for authenticated online players in the current single World.
- A configurable 600m square Cell grid.
- Shanghai-local meter coordinates for spatial calculations.
- Exact Euclidean distance checks after Cell candidate lookup.
- Symmetric visibility relationships.
- A 500m enter radius and 600m leave radius.
- AOI calculation and client replication at 10 Hz.
- Recalculation driven only by players that moved since the previous
  replication Tick.
- Separate initial messages for self state and visible entities.
- One batched `replication_update` per changed client per replication Tick.
- AOI-filtered position and appearance replication.
- An in-memory scale test with 1,000 test clients.

This phase excludes:

- Synthetic entities without WebSocket clients.
- NPC, monster, resource, or offline-player entity lifecycles.
- Million-entity benchmarks.
- A localhost WebSocket load generator.
- Multiple Hub or Shard actors.
- Geographic Shard ownership, cross-Shard migration, or boundary Ghosts.
- Gateway routing or distributed services.
- Global map projections, H3, or S2.
- A hard movement boundary around Shanghai.
- Server interaction AOI for combat, pickup, collision, or abilities.
- Client prediction, interpolation, or movement reconciliation.
- Backward compatibility with the old realtime protocol.

## Terminology

### AOI

AOI determines which public entities a client should receive. It is a client
replication concern in this phase, not an authorization rule for future combat
or interaction.

### Cell

A Cell is an in-memory square in the spatial index. Cells reduce the candidate
set for exact distance checks. A Cell is not a Room, Shard, process, or actor.

### Shard

A future Shard would authoritatively own a geographic region and its World
state. Sharding is not required to implement this AOI phase.

### Visibility

Two online players are visible to each other when one symmetric relationship
exists between their player IDs. A visible relationship means both clients
continue receiving relevant public state for the other player.

## Architecture

The implementation has three responsibilities:

### World

`game.World` remains the owner of authoritative player state and 20 Hz movement
simulation.

World:

- Applies input.
- Advances positions.
- Tracks which players moved since the previous replication Tick.
- Stores username and appearance.
- Does not know about WebSockets, JSON, Cells, or per-client visibility.

### AOIIndex

A dedicated `AOIIndex` owns spatial indexing and symmetric visibility
relationships.

AOIIndex:

- Converts authoritative latitude and longitude into Shanghai-local meter
  coordinates.
- Tracks each online player's current Cell and local position.
- Finds candidates from nearby Cells.
- Applies exact enter and leave distance rules.
- Maintains a symmetric adjacency set keyed by string player ID.
- Inserts, moves, and removes online players.
- Does not know about `Client`, WebSocket, JSON, or message queues.

### Hub

`Hub.Run()` remains the single actor that coordinates connections, World
mutations, AOI mutations, and client replication.

Hub:

- Calls World simulation at 20 Hz.
- Accumulates moved player IDs across simulation Ticks.
- At 10 Hz, updates AOI only for accumulated moved players.
- Applies relation changes to both sides.
- Builds client-specific replication changes.
- Sends at most one non-empty `replication_update` to each client per
  replication Tick.
- Keeps database operations outside the actor loop as in the existing design.

This separation lets the future load-testing phase reuse AOIIndex without
creating WebSocket clients.

## Spatial Model

### Coordinate Conversion

Latitude and longitude remain the authoritative protocol and persistence
format.

AOIIndex derives local meter coordinates relative to the configured Shanghai
World origin:

- Latitude uses the existing meters-per-degree latitude conversion.
- Longitude uses the origin latitude to calculate a fixed
  meters-per-degree-longitude scale.
- AOI distance uses squared Euclidean distance in local meters.

The approximation is intended for the configured Shanghai-area World. The
server does not prevent players from moving outside that area, but spatial
accuracy outside the intended region is not guaranteed by this phase.

### Cell Grid

Cell size is configurable and defaults to:

```text
600m x 600m
```

A Cell is identified by integer `(x, y)` coordinates derived from local meter
coordinates.

For an enter query, a player inspects its own Cell and the eight adjacent Cells.
With a 600m Cell and a 500m enter radius, these nine Cells contain every
possible entering candidate. Exact distance filtering removes false
positives.

Existing visible neighbors are checked separately from new candidates. This
ensures a fast move or future teleport cannot leave a stale relationship merely
because the former neighbor is no longer in the queried nine Cells.

## Visibility Rules

Visibility is symmetric:

```text
B in visible[A]  <=>  A in visible[B]
```

The relationship uses two thresholds:

- An invisible pair becomes visible at distance `<= 500m`.
- A visible pair remains visible through the 500m-600m hysteresis band.
- A visible pair becomes invisible at distance `> 600m`.

Therefore, at 550m:

- A pair that was already visible remains visible and continues receiving all
  relevant updates.
- A pair that was not visible remains invisible and receives no data.

There is no state where a marker remains visible but stops receiving updates.

The current player is never inserted into its own visibility set. Self state
uses a separate protocol path.

## Update Frequency

### Simulation Tick

At 20 Hz, every 50ms:

- World advances authoritative movement.
- Moved player IDs are accumulated.
- No Cell update, visibility calculation, or network replication occurs.

### Replication Tick

At 10 Hz, every 100ms:

1. Read all player IDs that moved during the preceding simulation Ticks.
2. Update their positions and Cell membership in AOIIndex.
3. Recalculate only relationships involving those moved players.
4. Apply each relationship change symmetrically.
5. Build client-specific replication results from the final relationship state.
6. Send one non-empty batch to each affected client.
7. Clear the moved and pending replication state for the completed Tick.

This phase implements client replication AOI only. Future server-side
interaction checks should use their own authoritative distance queries rather
than treating the 500m visible set as an interaction range.

## Recalculation Rules

For each moved player:

1. Query the nine nearby Cells for currently invisible enter candidates.
2. Establish a relationship for candidates now within 500m.
3. Inspect every existing visible neighbor.
4. Remove relationships whose current distance exceeds 600m.
5. Keep relationships in the hysteresis band unchanged.

When one side moves and the other remains still, both sides receive the same
relationship transition. The stationary side must not lag behind the moving
side.

If both players moved in the same period, processing the pair more than once
must be idempotent. It must not create duplicate entered or left results.

## Connection Lifecycle

### First Connection Or True Offline Reconnect

When the player does not exist in World:

1. Load persisted player state.
2. Add the player to World.
3. Insert the player into AOIIndex.
4. Establish symmetric visibility with players currently within 500m.
5. Immediately send the new client `self_state`.
6. Immediately send `visible_entities_snapshot` containing the complete public
   state of those visible players.
7. Queue the new player's complete state as `entered` for existing visible
   neighbors on the next replication Tick.

The new client does not receive duplicate `entered` entries for players already
present in its initialization snapshot. Its initialization snapshot consumes
and clears any pending replication changes addressed to that player ID.

### Same-Account Connection Replacement

When a new connection replaces the current connection for an existing player:

- Retain the World player.
- Retain Cell membership.
- Retain all symmetric visibility relationships.
- Retain position, username, and appearance.
- Reset input sequence as in the current behavior.
- Immediately send the replacement connection `self_state`.
- Immediately send a visible snapshot from the retained visibility set.
- Include retained visible players in the 500m-600m hysteresis band.
- Treat the snapshot as the replacement connection's current replication
  baseline and clear older pending changes addressed to that player ID.
- Do not queue `left` or `entered` changes for neighboring players.

### True Disconnect

When the current connection genuinely leaves:

1. Submit the final authoritative position using the existing persistence
   contract.
2. Remove the player from AOIIndex.
3. Remove every symmetric visibility relationship.
4. Queue the departed player ID in each still-connected former neighbor's
   `leftPlayerIds`.
5. Remove the player from World and the Hub client map.
6. Deliver the queued removals on the next replication Tick.

The client-side marker may therefore remain for at most approximately 100ms
after a genuine disconnect.

If both sides disconnect before the next replication Tick, no message needs to
be sent to either departed client.

## Realtime Protocol

This phase replaces the existing protocol in one backend/frontend release.
There is no dual-protocol compatibility or feature flag.

The following old server messages are removed:

- `world_snapshot`
- `players_delta`
- `appearance_changed`

### Self State

Sent immediately after a connection or replacement is registered:

```json
{
  "type": "self_state",
  "tick": 42,
  "player": {
    "id": "self-id",
    "username": "Alice",
    "lat": 31.2304,
    "lng": 121.4737,
    "appearance": {
      "color": "#3388ff",
      "shape": "circle"
    }
  }
}
```

`self_state` is a complete cold-state initialization message. It is not sent
periodically.

### Visible Entities Snapshot

Sent immediately after `self_state`:

```json
{
  "type": "visible_entities_snapshot",
  "tick": 42,
  "players": [
    {
      "id": "other-id",
      "username": "Bob",
      "lat": 31.2308,
      "lng": 121.4737,
      "appearance": {
        "color": "#ff6600",
        "shape": "diamond"
      }
    }
  ]
}
```

The snapshot never contains the current player.

For a first connection or true reconnect, it contains players whose newly
established relationship is within 500m. For a same-account replacement, it
uses the retained visibility set and may contain players in the 500m-600m
hysteresis band.

### Replication Update

At each replication Tick, an affected client receives at most one message:

```json
{
  "type": "replication_update",
  "tick": 44,
  "selfPosition": {
    "lat": 31.2305,
    "lng": 121.4737
  },
  "entered": [
    {
      "id": "entered-id",
      "username": "Carol",
      "lat": 31.2310,
      "lng": 121.4738,
      "appearance": {
        "color": "#00aa66",
        "shape": "triangle"
      }
    }
  ],
  "leftPlayerIds": ["left-id"],
  "positions": [
    {
      "id": "visible-id",
      "lat": 31.2307,
      "lng": 121.4739
    }
  ],
  "appearances": [
    {
      "playerId": "visible-id",
      "appearance": {
        "color": "#8844ff",
        "shape": "square"
      }
    }
  ]
}
```

Fields without changes are omitted. If every field would be omitted, the
server skips the entire message.

`selfPosition` is included only when the current player's authoritative
position changed during the period. It does not include the player ID.

`entered` contains complete public player state.

`leftPlayerIds` contains only IDs.

`positions` contains only positions for other players that:

- Were visible before the Tick.
- Remain visible after the Tick.
- Moved during the period.

`appearances` contains the final appearance for the current player or other
players that remain visible at the end of the Tick.

## Replication Precedence

The server builds each client's update from final state, not from an ordered
history of intermediate events.

For any other player ID, the fields are mutually exclusive:

1. `leftPlayerIds` wins over all other public updates.
2. `entered` supplies complete state and excludes the same player from
   `positions` and `appearances`.
3. `positions` and `appearances` may both describe a player that remained
   visible and changed both kinds of state.

The current player's ID never appears in `entered`, `leftPlayerIds`, or
`positions`. Its movement uses `selfPosition`. Its appearance may appear in
`appearances`.

Repeated appearance changes during one 100ms period collapse to the final
authoritative appearance.

Because AOI is calculated once at the replication Tick, there is no queue of
intermediate enter and leave events to replay. The client receives only the
final visible result for that Tick.

## Appearance Updates

The existing HTTP persistence ordering remains:

1. Validate and persist the appearance.
2. Submit an update event to Hub.
3. Wait until Hub applies the authoritative World change.
4. Return the HTTP response.

Hub does not immediately send a standalone WebSocket message. It marks the
player's appearance as pending replication.

At the next replication Tick:

- The player receives its own final appearance.
- Players visible at the end of the Tick receive the appearance.
- A newly visible player gets the appearance through complete `entered` state,
  not a duplicate `appearances` entry.
- A player that left visibility receives only `leftPlayerIds`.
- Invisible players receive nothing.

The HTTP response does not wait for the next 10 Hz replication Tick. Remote
clients may observe the new appearance up to approximately 100ms later.

## Client Behavior

The browser handles protocol messages as follows:

- `self_state`: initialize authoritative current-player state and marker.
- `visible_entities_snapshot`: replace every other visible marker with the
  supplied set.
- `replication_update.selfPosition`: move the current marker and preserve map
  following.
- `entered`: create complete markers.
- `leftPlayerIds`: remove markers.
- `positions`: move existing visible markers without changing appearance.
- `appearances`: update existing marker appearance without changing position.

The browser applies one `replication_update` as one logical Tick. It should
process removals and complete entries consistently with the server precedence
rules rather than relying on array ordering to resolve duplicate IDs.

This phase does not add visual interpolation. The browser continues receiving
authoritative position samples at up to 10 Hz.

## Error And Backpressure Behavior

- Failure to enqueue either initialization message disconnects the new client.
- Failure to enqueue a periodic replication update uses the existing
  slow-client disconnection behavior.
- Encoding failures are logged and handled consistently with the current Hub
  message path.
- AOIIndex does not silently repair asymmetric or missing relationships.
  Correctness is enforced through actor ownership and tests.
- The server does not add fallback global broadcasts when AOI state is
  unavailable.
- AOIIndex is derived in-memory state and is not persisted or drained during
  shutdown.

Existing logout, final position persistence, same-account replacement, and
graceful shutdown ordering remain authoritative.

## Observability

The existing periodic realtime statistics are extended with AOI and replication
counts:

- Online clients.
- Moved players processed.
- Candidate pairs checked.
- Exact distance checks.
- Relationships entered.
- Relationships left.
- Non-empty replication messages.
- Replication recipients.
- Encoded replication payload bytes.

These statistics support correctness and later benchmark design. This phase
does not define a strict latency service-level objective.

## Testing

### AOIIndex Unit Tests

- Insert a player into the expected Cell.
- Move a player within a Cell and across a Cell boundary.
- Remove a player and all associated relationships.
- Query all and only relevant nine-Cell candidates.
- Establish a symmetric relationship at exactly 500m.
- Do not establish a new relationship between 500m and 600m.
- Preserve an existing relationship between 500m and 600m.
- Remove a symmetric relationship beyond 600m.
- Update the stationary side when only one player moves.
- Handle both players moving without duplicate relation changes.
- Check existing neighbors independently of the new-candidate Cells.

### World And Protocol Tests

- Accumulate players moved across two simulation Ticks and consume them at one
  replication Tick.
- Encode exact `self_state`, visible snapshot, and replication JSON contracts.
- Omit empty optional fields.
- Skip an entirely empty replication update.
- Keep self out of public entity collections.
- Enforce entered, left, position, and appearance precedence.

### Hub Tests

- First connection receives self and a `<=500m` local snapshot.
- Existing neighbors receive the new player in the next Tick.
- A stationary player receives entered or left when the other side moves.
- A same-account replacement retains Cell and visibility relationships.
- A replacement snapshot includes retained 500m-600m relationships.
- A true disconnect queues left updates for connected neighbors.
- A true offline reconnect rebuilds only `<=500m` relationships.
- Only moved players trigger relation recalculation.
- Only currently visible players receive public position and appearance data.
- Appearance changes collapse to the final value for the Tick.
- New entries do not duplicate position or appearance data.
- Empty per-client updates are skipped.
- Slow clients are disconnected.
- Position persistence, logout, and graceful shutdown behavior do not regress.

### Frontend Manual Verification

- Two players appear when moving within 500m.
- They remain visible while moving through the 500m-600m band.
- They disappear after moving beyond 600m.
- Movement and appearance updates stop after leaving visibility.
- A page refresh preserves nearby markers without flashing for other clients.
- A true logout and login rebuilds visibility using the 500m enter threshold.
- Current-player marker and map following continue to work.
- Appearance saves update the owner and visible neighbors on the next
  replication Tick.

### 1,000-Client Scale Test

Use in-memory test clients and deterministic positions to exercise:

- 1,000 connected online players.
- A mix of stationary and moving players.
- Enter, leave, and hysteresis transitions.
- A deliberately dense local region.
- Strict filtering so each client receives only current visible entities.

The test records:

- Candidate checks.
- Relationship changes.
- Replication message count.
- Encoded payload bytes.

It does not enforce a strict millisecond threshold because CI hardware varies.
It is a functional scale test, not evidence of production capacity.

Million-entity and real localhost WebSocket load tests belong to the separate
AOI load-testing design.

## Acceptance Criteria

The phase is complete when:

- Global snapshots and global deltas are replaced by AOI-filtered replication.
- Self state is initialized and replicated separately from public entities.
- Visibility is symmetric and follows the 500m/600m hysteresis rules.
- Only moved players cause relationship recalculation.
- Stationary relationship peers receive matching transitions.
- Each changed client receives at most one replication message per 100ms Tick.
- Empty client updates are skipped.
- Public position and appearance changes never reach invisible clients.
- Same-account replacement preserves visibility without neighbor flicker.
- True disconnect and reconnect semantics match the approved design.
- The 1,000-client in-memory scale test passes.
- Existing authentication, appearance persistence, connection reliability,
  final position persistence, logout, and graceful shutdown behavior remains
  correct.
- `go test ./...` passes.
- `go vet ./...` passes.
