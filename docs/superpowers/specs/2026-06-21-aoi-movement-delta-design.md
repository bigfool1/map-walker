# AOI Movement Delta Design

2026-06-21

## Problem

After `ReplicationDispatcher` and synchronous `ReplicationBuilder`, benchmark
evidence shows builder fanout is not the current bottleneck:

```text
HubReplication 2000 clients: ~13.7ms/op
ReplicationBuilder fanout: ~1.5ms/op (~11%)
Dispatcher encode/send: ~0.5ms/op with 8 workers
Immutable job copying: ~0.2ms/op
```

The next hot path is AOI and visibility work inside the Hub actor:

- `snapshotMoverVisibility`
- `applyMovementAOIChanges`
- `AOIIndex.recalculateRelationships`
- repeated `VisibleNeighbors` reads needed for stable position fanout
- `recalcCollectibleVisibility`

The current information flow is thin:

```text
Hub snapshots old visible neighbors
Hub calls AOIIndex.Move(player)
AOIIndex.Move returns only entered/left
ReplicationBuilder asks AOIIndex.VisibleNeighbors(player) again
Builder intersects old neighbors with new visible neighbors to find stable fanout
```

This creates extra map copies and repeated visibility reads around the same
movement event. Before changing AOI geometry or implementing a boundary-aware
incremental algorithm, the movement path should expose the relationship delta
already needed by replication.

## Goal

Add an AOI movement-delta path that returns entered, left, and stable neighbors
from the same movement update, then use that delta in Hub and
`ReplicationBuilder` to remove `snapshotMoverVisibility` and repeated stable
neighbor `VisibleNeighbors` lookups.

## Non-Goals

- Do not change AOI geometry.
- Do not change 500m enter / 600m leave hysteresis.
- Do not change symmetric relationship semantics.
- Do not change cell size or nine-cell candidate coverage.
- Do not introduce spatial shards.
- Do not parallelize AOI.
- Do not implement boundary-aware incremental AOI skipping in this phase.
- Do not change collectible grid algorithms in this phase.
- Do not change WebSocket protocol fields.

## Design

Add a detailed movement API to `internal/game/AOIIndex`.

Conceptual shape:

```go
type MovementDelta struct {
    PlayerID int64
    Entered  []int64
    Left     []int64
    Stable   []int64
}
```

`Entered` and `Left` preserve the existing `RelationshipChanges` meaning:

- `Entered`: neighbors that became visible during this movement
- `Left`: neighbors that stopped being visible during this movement

`Stable` contains neighbors that were visible before the movement and remain
visible after the movement. It is intended for position fanout: these recipients
need the mover's new position but do not need an entered event.

The existing `Move` method can remain for callers that only need
`RelationshipChanges`. It may be implemented as a wrapper over the detailed
path.

## AOIIndex Behavior

`MoveDetailed(playerID, lat, lng)` should:

1. Return an empty delta if the player does not exist.
2. Capture the player's old visible neighbor IDs before movement without
   allocating a nested map per mover.
3. Move the player into the new position and cell.
4. Recalculate relationships using the same enter/leave rules as today.
5. Return:
   - entered neighbors from the recalculation
   - left neighbors from the recalculation
   - stable neighbors from old visible neighbors minus left neighbors

The first implementation may allocate slices for `Stable`, but it should avoid
the current `map[int64]map[int64]struct{}` snapshot shape in Hub.

Ordering remains not part of AOI behavior. Tests should use set equality.

## Hub Integration

Replace the current sequence:

```text
oldNeighborsByMover := snapshotMoverVisibility(movedIDs)
applyMovementAOIChanges(movedIDs)
builder.Build(input with oldNeighborsByMover, reader.VisibleNeighbors)
```

with:

```text
movementDeltas := applyMovementAOIDeltas(movedIDs)
builder.Build(input with movementDeltas)
```

`applyMovementAOIDeltas` remains actor-owned and still:

- reads player positions from `World`
- calls AOI movement
- reads player state from `World` for entered events
- filters pending entered/left by connected clients
- records AOI stats through the existing `AOIIndex.TakeStats` path

The key change is that stable-neighbor fanout should use
`MovementDelta.Stable` rather than asking AOI for current visible neighbors and
intersecting with a copied old-neighbor set.

## ReplicationBuilder Integration

Change `ReplicationBuildInput` so moved-player information is carried as
movement deltas instead of:

```text
movedIDs
oldNeighborsByMover
reader.VisibleNeighbors(moverID)
```

The builder should use:

```text
for each movement delta:
  send self position to the mover if connected
  send position to connected stable neighbors
```

Entered and left player events may continue to flow through pending-entered and
pending-left inputs, or the builder may receive them from movement deltas if
that keeps Hub simpler. The implementation plan should choose the smaller
change that preserves behavior.

## Metrics

Add or preserve enough timing to distinguish:

- AOI detailed movement duration
- stable-neighbor delta construction duration
- builder fanout duration
- collectible visibility recalculation duration
- replacement fanout duration

At minimum, the benchmark report must compare before/after:

- `BenchmarkHubReplication` ns/op
- AOI candidate pairs
- AOI distance checks
- relationships entered/left
- replication messages
- replication bytes
- allocations

## Tests

Required AOI tests:

- `MoveDetailed` entered/left matches existing `Move` for deterministic
  scenarios.
- `Stable` contains old-and-still-visible neighbors.
- `Stable` excludes newly entered neighbors.
- `Stable` excludes left neighbors.
- `Stable` is empty for an unknown player.
- Existing AOI hysteresis tests still pass.
- Existing symmetric relationship tests still pass.

Required realtime tests:

- Hub no longer needs `snapshotMoverVisibility` for stable position fanout.
- Stable neighbors still receive moved-player position updates.
- Newly entered neighbors receive entered events, not duplicate position-only
  stable updates.
- Left neighbors receive left events and no stable position update for the same
  movement.
- Existing replication benchmark message/byte counts remain logically
  comparable.

## Post-Phase Decision Rule

After movement delta integration, profile again.

Choose the next step by evidence:

- If AOI movement time drops and remaining cost is collectible visibility,
  optimize collectible visibility recalculation next.
- If AOI movement still dominates and candidate/distance counts remain high,
  design boundary-aware incremental AOI movement.
- If relationship map lookups dominate, consider dense relationship storage or
  integer-indexed player IDs.
- If builder or immutable copy costs become dominant, return to replication
  data-layout optimization.

## Follow-Up Path

This phase deliberately stops at movement-delta plumbing:

```text
Current:
Hub copies old neighbors -> AOI Move -> Builder asks VisibleNeighbors

This phase:
AOI MoveDetailed -> Hub movement deltas -> Builder stable fanout
```

Future algorithmic AOI work should be specified separately after this phase
shows whether removing repeated snapshot/visibility reads is enough.
