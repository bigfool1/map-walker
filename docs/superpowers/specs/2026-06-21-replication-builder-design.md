# Replication Builder Design

2026-06-21

## Problem

`ReplicationDispatcher` has moved per-recipient JSON encoding and
`ClientSender.Send` out of the Hub actor. The remaining broadcast hot path is
the per-recipient fanout inside `broadcastReplication`:

- self position fanout for moved connected players
- stable-neighbor position fanout through `VisibleNeighbors`
- entered player fanout
- left player fanout
- appearance fanout to self and visible neighbors
- collectible entered/left/spawned/collected fanout
- `byRecipient` map creation and append-heavy accumulation
- immutable `replicationJob` construction and `copyReplicationChanges`

This logic still lives in `hub.go` and mixes three responsibilities:

1. Hub actor state orchestration.
2. Replication recipient selection.
3. `ReplicationChanges` and immutable job construction.

Synchronous extraction will not by itself reduce Hub actor CPU, because the
builder still runs on the actor goroutine. Its purpose is to create locality,
testability, and a profile seam so the next optimization can be chosen from
evidence rather than architecture guesswork.

## Goal

Extract per-recipient replication fanout into a synchronous
`ReplicationBuilder` module and add metrics that separate AOI work, fanout
work, immutable job construction, and dispatcher encode/send pressure.

## Non-Goals

- Do not introduce spatial shards.
- Do not introduce async builder workers in this phase.
- Do not add builder queues, coalescing, stale tick handling, or builder
  backpressure policy in this phase.
- Do not add `ReplicationBuildSnapshot` in this phase.
- Do not parallelize `World`, `AOIIndex`, or Hub actor state mutation.
- Do not move `applyMovementAOIChanges`, `snapshotMoverVisibility`, or
  collectible visibility recalculation out of the actor.
- Do not change `replication_update` protocol fields.
- Do not change `ReplicationDispatcher` encode/send behavior.
- Do not change browser client behavior.

## Design

Add a synchronous `ReplicationBuilder` module under `internal/realtime/`.

Hub remains the only owner of `World`, `AOIIndex`, connected clients, and
pending replication buffers. `broadcastReplication` still performs actor-owned
state steps:

```text
movedIDs := world.TakeMovedPlayerIDs()
world.TakeRemovedPlayerIDs()
oldNeighborsByMover := snapshotMoverVisibility(movedIDs)
applyMovementAOIChanges(movedIDs)
pendingEntered := takePendingEntered()
pendingLeft := takePendingLeft()
pendingAppearances := takePendingAppearances()
advanceCollectibleReplacements()
recalcCollectibleVisibility(movedIDs)
collectEntered/Left/Spawned/Collected := takePending...
```

After these steps, Hub calls:

```text
jobs := builder.Build(input, reader)
```

`ReplicationBuilder` owns the current `byRecipient` accumulation logic. It does
not encode JSON and does not send bytes; it returns immutable jobs for the
existing dispatcher.

After this phase, `broadcastReplication` should read as orchestration:

```text
gather actor-owned changes
jobs := builder.Build(input, hubReader)
submit jobs to dispatcher
```

## Reader Interface

This phase uses a synchronous reader interface that is only valid while called
on the Hub actor goroutine:

```text
Connected(playerID) bool
Client(playerID) (ClientSender, bool)
VisibleNeighbors(playerID) []int64
PlayerPosition(playerID) (game.PlayerPosition, bool)
```

The reader may adapt current Hub fields. It must not be stored by the builder
or used outside `Build`.

This reader is intentionally not an async-safe seam. If profiling later shows
fanout construction is still the dominant actor-owned bottleneck, a follow-up
design can replace the reader with an immutable snapshot.

The reader shape is a hypothesis, not a permanent abstraction. The direct
builder benchmark must compare at least two reader variants:

- interface reader: `ReplicationBuildReader`
- concrete reader: a concrete struct holding the current `*game.AOIIndex`,
  client map, and required player-position access

If the interface reader shows a measurable `ns/op` or `allocs/op` regression in
the hot path, switch the production builder to the concrete reader or explicit
function fields before completing this phase.

## Build Input

`ReplicationBuildInput` contains only values already gathered by the actor:

```text
tick
movedIDs
oldNeighborsByMover
pendingEntered
pendingLeft
pendingAppearances
collectEntered
collectLeft
collectSpawned
collectCollected
```

The builder returns no jobs when all input collections are empty.

## Builder Output

The builder returns `[]replicationJob`.

Each job must be safe for dispatcher workers:

- `ReplicationChanges` are copied with the existing `copyReplicationChanges`
  helper before leaving the builder.
- Disconnected recipients are skipped through the reader.
- The builder does not submit to the dispatcher.
- The builder does not mutate Hub state.

## Metrics

Add builder metrics separate from dispatcher metrics:

- build calls
- jobs produced
- recipients touched
- changes appended
- by-recipient accumulation duration
- immutable copy duration
- total build duration
- optional allocations from direct benchmarks

These metrics should make it clear whether the remaining actor-side bottleneck
is:

- AOI work: `snapshotMoverVisibility`, `applyMovementAOIChanges`,
  `VisibleNeighbors`, `recalcCollectibleVisibility`
- fanout work: `ReplicationBuilder.Build`, `byRecipient` map operations,
  recipient checks, appends
- immutable job work: `copyReplicationChanges`, slice copies, allocations
- dispatcher pressure: queue depth, dropped jobs, encode/send throughput

Keep replication counter semantics explicit:

- `ReplicationRecipients` means builder-produced recipient jobs submitted by
  Hub to the dispatcher. It can include jobs later dropped by dispatcher
  backpressure, skipped as empty, or failed during encode/send.
- `Dispatcher.Encoded` means actual non-empty messages successfully encoded.
- `ReplicationMessages` is the dispatcher encoded-message count.
- `ReplicationBytes` is dispatcher encoded bytes.

Do not use `ReplicationRecipients` as "messages actually sent" after dispatcher
offload.

## Post-Phase Decision Rule

After synchronous builder extraction, run Hub replication benchmarks and
synthetic load profiling before choosing the next optimization.

Choose the next step by evidence:

- If time is mostly in AOI, `VisibleNeighbors`, or collectible visibility,
  optimize AOI/fanout algorithms before adding async builder workers.
- If time is mostly in `ReplicationBuilder.Build` and immutable snapshot
  construction is expected to be affordable, design a snapshot seam and async
  builder prototype.
- If time is mostly in `copyReplicationChanges` or immutable job construction,
  optimize data layout and copy strategy before adding async workers.
- If dispatcher queue depth or dropped jobs rise, tune dispatcher worker count,
  queue sizing, or encode/send backpressure before increasing upstream
  production.

Async builder work must be specified separately with bounded queues, dispatcher
backpressure interaction, stale tick policy, and event coalescing. It is not
part of this phase.

## Tests

Required tests:

- Builder returns the same recipient changes as the current in-Hub fanout for
  moved players, entered players, left players, appearances, and collectibles.
- Builder returns no jobs for empty input.
- Builder does not retain the synchronous reader after `Build`.
- Jobs returned by builder contain copied `ReplicationChanges` suitable for
  dispatcher workers.
- `broadcastReplication` continues to submit equivalent jobs to dispatcher.

Required performance checks:

- Run `go test ./internal/realtime`.
- Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- Add direct builder benchmarks for interface reader and concrete reader
  variants.
- Direct builder benchmarks must report recipients, jobs, changes appended,
  by-recipient accumulation duration, copy duration, total build duration, and
  allocations.
- Use builder metrics plus dispatcher metrics to decide the next optimization.

## Follow-Up Path

This design intentionally stops at the synchronous builder:

```text
Current:
Hub actor -> in-Hub byRecipient fanout -> dispatcher

This phase:
Hub actor -> synchronous ReplicationBuilder -> dispatcher
```

The next phase is not predetermined. It should be chosen after profiling:

```text
Candidate A:
AOI / collectible / fanout algorithm optimization

Candidate B:
immutable ReplicationBuildSnapshot -> async builder workers

Candidate C:
immutable job data-layout and copy optimization

Candidate D:
dispatcher backpressure / worker tuning
```
