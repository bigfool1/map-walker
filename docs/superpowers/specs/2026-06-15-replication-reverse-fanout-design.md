# Replication Reverse Fan-Out Optimization Design

Date: 2026-06-15

## Goal

Replace the Hub replication hot path's all-client-by-all-mover scan with
recipient-oriented reverse fan-out from each changed player.

The optimization must preserve current WebSocket protocol behavior while
significantly reducing broadcast Tick duration in deterministic 2,000- and
3,000-client benchmarks. This phase does not need to restore stable 20 Hz
simulation and 10 Hz replication under the full service load.

## Evidence

A service run near 2,800 connected clients reported:

- 8-10 simulation Ticks per second instead of the configured 20.
- Approximately 11,000-17,000 replication messages per second.
- Approximately 18-27 MB of replication JSON per second.
- Approximately 800,000-1,200,000 AOI distance checks per second.

The current `broadcastReplication` implementation:

1. Copies every connected client's visible-neighbor set.
2. Applies AOI updates for moved players.
3. Iterates every connected client.
4. For each client, iterates every moved player and checks old and current
   visibility.
5. Builds and encodes one client-specific replication message when non-empty.

With roughly 2,800 clients and roughly 2,000 moved players per broadcast, the
client-by-mover scan performs millions of visibility lookups per broadcast
before JSON encoding and send-queue work.

The Hub actor is serial even when the Go runtime and machine have idle CPU
capacity. A broadcast taking longer than its 100ms interval delays simulation,
input, registration, persistence, and later broadcast events without requiring
whole-machine CPU utilization to reach 100 percent.

## Scope

This phase includes:

- Adding deterministic 2,000- and 3,000-client Hub replication benchmarks.
- Recording a pre-change baseline for Tick duration and allocation.
- Replacing the full visibility snapshot with old-neighbor snapshots for moved
  players only.
- Building replication changes by recipient from movers, pending entry,
  pending leave, and pending appearance events.
- Encoding and sending only for recipients that accumulated changes.
- Adding regression coverage for multi-mover and same-Tick conflict behavior.
- Comparing optimized benchmark results with the recorded baseline.

This phase excludes:

- Changing the WebSocket protocol.
- Changing AOI geometry, hysteresis, or spatial indexing.
- Changing current movement-induced entry and leave semantics.
- Moving JSON encoding or socket writes out of the Hub actor.
- Increasing send-buffer capacity.
- Changing queue-full disconnection behavior.
- Changing final position persistence.
- Restoring stable 20 Hz simulation and 10 Hz replication as a required result.
- Frontend marker or map rendering optimization.

## Behavioral Contract

The following externally observable behavior remains unchanged:

- A moved client receives its authoritative position as `selfPosition`.
- An observer receives a moved player's `position` only when the visibility
  relationship existed both before and after the broadcast's AOI update.
- A newly visible player is represented by `entered`, not by a duplicate
  `position`.
- A player that left visibility is represented by `leftPlayerIds`, not by a
  duplicate `position` or `appearance`.
- Pending connection entry, disconnection leave, and appearance changes are
  consumed once at the broadcast boundary.
- Empty replication updates are not encoded or sent.
- Queue-full clients are removed using the existing disconnection path.
- Replication output remains normalized, deduplicated, and ordered by player
  ID at the protocol boundary.

### Existing Movement Entry And Leave Semantics

This optimization preserves the current Hub behavior exactly.

When a mover creates a new AOI relationship, the currently connected neighbor
receives the mover's full state through `entered`. The mover does not
necessarily receive the neighbor as a movement-induced `entered` event in the
same broadcast.

When a mover breaks an AOI relationship, the currently connected former
neighbor receives the mover's ID through `leftPlayerIds`. The mover does not
necessarily receive the former neighbor as a movement-induced leave event in
the same broadcast.

Changing those semantics would be a separate protocol-behavior change with its
own design and tests.

## Recipient Accumulation

Each broadcast creates a local map keyed by recipient player ID:

```go
map[int64]*ReplicationChanges
```

Entries are created only when a recipient has a candidate change. The existing
`NormalizeReplicationChanges` function remains the final authority for:

- Removing self references.
- Suppressing positions and appearances for entered or left players.
- Deduplicating leave IDs and appearance updates.
- Sorting wire collections by player ID.

The accumulator does not introduce a new persistent Hub field or cross-Tick
cache.

## Broadcast Data Flow

### 1. Capture Mover-Local Old Visibility

After taking moved player IDs from World and before applying AOI movement, the
Hub captures `VisibleNeighbors` only for those moved players.

The old-neighbor snapshots are sets keyed by mover ID. The Hub no longer copies
the visible-neighbor set for every connected client.

### 2. Apply AOI Movement

The Hub applies existing AOI movement updates in moved-player order.

`applyMovementAOIChanges` continues to populate the existing pending buffers:

- `pendingEntered`
- `pendingLeft`

No AOI API or relationship semantics change in this phase.

### 3. Accumulate Self Positions

For each moved player that is still connected, the accumulator records the
player's current authoritative coordinates as `SelfPosition` for that player's
recipient entry.

### 4. Accumulate Stable-Relationship Positions

For each moved player:

1. Read the player's final visible neighbors after all AOI moves have been
   applied.
2. For each final neighbor, check membership in that mover's old-neighbor set.
3. If the relationship existed both before and after movement and the neighbor
   is still connected, append the mover's current position to that neighbor's
   accumulated `Positions`.

This replaces the current connected-client-by-moved-player nested loop.

### 5. Accumulate Pending Entries

After taking the current `pendingEntered` states, the Hub iterates each entered
player's final visible neighbors.

Each still-connected neighbor receives that player's full state in `Entered`.
This matches the current implementation, which tests each pending entered
player against every connected client's final visibility.

The entered player is not automatically given every neighbor's state.

### 6. Accumulate Pending Leaves

The existing `pendingLeft` map is already keyed by recipient. Its player IDs
are appended directly to the corresponding recipient's accumulated
`LeftPlayerIDs`.

Recipients that disconnected before send are skipped.

### 7. Accumulate Pending Appearances

For each pending appearance:

- The changed player receives its own appearance update when connected.
- Each currently visible, connected neighbor receives the appearance update.

This matches the current visibility condition:

```text
playerID == recipientID OR currently visible to recipient
```

`NormalizeReplicationChanges` continues to suppress appearance updates that
conflict with an entry or leave in the same message.

### 8. Encode And Send

The Hub iterates only recipient IDs present in the accumulator.

For each still-connected recipient:

1. Call `TryEncodeReplicationUpdate`.
2. Update the existing replication message, recipient, and byte counters.
3. Send through the existing bounded client queue.
4. Remove the client through the existing path if `Send` reports queue full.

Map iteration order is not protocol behavior because message contents remain
normalized and sorted. No guarantee is added about cross-client send order.

## Same-Tick Movement Semantics

Multiple players may move during the same broadcast interval. AOI updates are
applied sequentially by the Hub, as they are today.

The optimization records each mover's old neighbor set before any movement is
applied, then reads final neighbors after all movement is applied. Therefore:

- A relationship present at both broadcast boundaries receives position
  replication.
- A relationship absent before and present after is handled by the existing
  pending entry path.
- A relationship present before and absent after is handled by the existing
  pending leave path.
- Intermediate relationships created and removed within the same broadcast
  remain subject to existing pending-buffer and normalization behavior.

Regression tests must freeze the current output for multi-mover crossing,
entry, leave, and conflict scenarios before replacing the algorithm.

## Benchmark Design

Add a benchmark in `internal/realtime` that exercises real:

- World movement.
- AOI updates.
- Recipient-specific replication assembly.
- JSON encoding.
- Bounded send queues with immediate draining.

It excludes HTTP, WebSocket framing, kernel socket buffers, browser parsing,
and frontend rendering.

### Scenarios

The benchmark runs at:

- 2,000 connected clients.
- 3,000 connected clients.

Placement follows the synthetic client's deterministic 10km by 10km activity
region with a fixed seed and stable account IDs.

The benchmark uses a fixed movement ratio and deterministic input pattern so
the same moved-player set, AOI relationships, replication messages, and
replication bytes can be compared before and after the implementation.

### Measurements

Record:

- `ns/op`
- `B/op`
- `allocs/op`
- moved players per operation
- AOI relationship changes
- replication messages
- replication bytes

The pre-change baseline and post-change result must use the same commit build
settings, Go version, machine, scenario parameters, and benchmark count.

## Testing

### Regression Tests

Add or strengthen Hub tests for:

- Multiple stable visible movers updating the same recipient.
- Two movers entering visibility in the same broadcast.
- Two movers leaving visibility in the same broadcast.
- A player entering and moving in the same broadcast without duplicate
  `position`.
- A player leaving with a pending appearance without duplicate `appearance`.
- A mover receiving exactly one `selfPosition`.
- A disconnected or queue-full recipient being removed while accumulated
  recipients remain sendable.
- Connection replacement retaining current behavior.
- Deterministic logical message content across repeated runs.

Existing initialization, hysteresis, appearance, disconnection, replacement,
stats, and 1,000-client functional tests remain required.

### Project Verification

Run:

```bash
go test ./internal/realtime
go test ./...
go vet ./...
```

Run the focused benchmark before and after the implementation with allocation
reporting and enough repetitions to distinguish the result from run-to-run
noise.

## Success Criteria

The phase succeeds when:

- Existing and new realtime tests pass.
- Full project tests and vet pass.
- Replication protocol behavior remains logically identical for the frozen
  benchmark and regression scenarios.
- Replication message and byte totals remain identical for the same benchmark
  workload.
- The 2,000- and 3,000-client benchmarks show a clear reduction in broadcast
  Tick duration.
- Allocation results are reported honestly, whether improved, unchanged, or
  regressed.

Restoring stable service-level 20 Hz simulation and 10 Hz replication is not a
success requirement for this phase. Remaining bottlenecks become evidence for
subsequent JSON encoding, persistence, send-queue, or transport work.

## Documentation

After implementation and measurement:

- Record the baseline and optimized benchmark comparison under
  `docs/benchmarks/`.
- Update `docs/map-walker-handoff.md` with the new replication architecture,
  measured result, and remaining bottlenecks.
- Keep README protocol examples unchanged unless verification finds an actual
  protocol mismatch.
