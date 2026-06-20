# Replication Offload Design

2026-06-21

## Problem

Production load with mock WebSocket users shows broadcast ticks gradually fail
to maintain 20 Hz as connected user count rises, and can fall into single-digit
Hz. The current Hub actor remains authoritative, but `broadcastReplication`
does too much work in the actor goroutine:

1. Advances actor-owned state: moved player IDs, AOI relationships, pending
   player/appearance changes, collectible visibility, and collectible spawn or
   collect fanout.
2. Builds per-recipient `ReplicationChanges` through the `byRecipient` map.
3. Encodes each recipient update with `TryEncodeReplicationUpdate`.
4. Enqueues encoded bytes through `ClientSender.Send`.
5. Removes clients synchronously when `Send` fails.

The first two groups depend on actor-owned `World`, `AOIIndex`, pending maps,
and the connected client map. They should stay in the actor until a later
design introduces explicit immutable replication deltas.

The last three groups can be moved behind a narrow interface once the actor
hands off immutable per-recipient jobs.

## Goal

Reduce broadcast tick time by moving replication encoding, send-buffer enqueue,
and send-failure feedback out of the Hub actor while preserving server
authority and WebSocket protocol semantics.

## Non-Goals

- Do not introduce world shards.
- Do not parallelize `World` or `AOIIndex`.
- Do not move `byRecipient` construction out of the actor in this phase.
- Do not change `replication_update` protocol fields.
- Do not change browser client behavior.
- Do not change MySQL, Redis, or persistence behavior.
- Do not add global broadcast semantics; replication remains per recipient.

## Design

Add a `replicationDispatcher` module in `internal/realtime/`.

The Hub actor remains the only writer of authoritative state. During
`broadcastReplication`, the actor still gathers movement, AOI, appearance, and
collectible changes, and still builds `map[int64]*ReplicationChanges`.

At the final per-recipient loop, the actor no longer calls
`TryEncodeReplicationUpdate` or `client.Send` directly. Instead it submits a
`replicationJob` to the dispatcher:

```text
recipientID
client ClientSender
tick
changes ReplicationChanges
```

The dispatcher owns a small worker pool. Each worker:

1. Encodes the job with `TryEncodeReplicationUpdate`.
2. Skips empty updates.
3. Calls `client.Send(data)`.
4. Reports send failure back to the Hub through a callback that requests actor
   disconnect handling.

The callback must not call `removeClient` directly. It should use an existing
actor-owned path such as `DisconnectUser` or an equivalent non-blocking
disconnect intent channel owned by Hub.

## Immutability Requirement

`ReplicationChanges` must become immutable before it leaves the actor.

The first implementation should prefer correctness over minimal allocation:
copy all slice fields in the submitted `ReplicationChanges` so dispatcher
workers never observe actor-owned slices that may later be reused or mutated.

This phase should not pool or reuse job data unless tests prove the reuse does
not cross the actor/worker ownership boundary.

## Backpressure

The dispatcher queue is bounded.

If the queue is full, the actor should drop the replication job for that
recipient and record a drop counter. It must not block the broadcast tick
waiting for dispatcher capacity.

This mirrors the existing client send buffer policy: when a recipient cannot
keep up, the system favors bounded memory and continued tick progress over
unbounded buffering.

The first phase should not disconnect a client only because one dispatcher job
is dropped. A later policy can consider repeated drops per recipient.

## Ordering

The current protocol carries a tick number. The browser must tolerate receiving
updates over time, but per-recipient ordering should still be preserved.

The first implementation should choose one of these:

- Use a deterministic recipient-to-worker partition so each recipient's jobs
  are encoded and sent by the same worker in submission order.
- Or keep a single dispatcher worker for phase one, proving actor offload first,
  then add partitioned workers in a later plan.

The implementation plan should prefer deterministic recipient partitioning if
it stays small. If it becomes complex, single-worker offload is acceptable for
the first commit because it still removes encode/send from the actor goroutine.

## Stats

Add dispatcher counters to `HubSnapshot` or an adjacent profile snapshot:

- submitted jobs
- dropped jobs
- encoded messages
- skipped empty jobs
- encode errors
- send failures
- queue depth
- worker count
- encoded bytes

These counters should be included in stats output used by synthetic load tests.

## Tests

Required behavior tests:

- Dispatcher encodes and sends a non-empty replication job.
- Dispatcher skips an empty replication job without sending.
- Dispatcher reports send failure through the Hub-owned disconnect path.
- Submitted `ReplicationChanges` are immutable from the worker's perspective.
- Bounded queue drops are counted and do not block the caller.
- Per-recipient ordering is preserved by the chosen dispatch strategy.
- `broadcastReplication` still produces the same replication messages for the
  existing benchmark scenario.

Required performance checks:

- Run `go test ./internal/realtime`.
- Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- Compare replication messages, bytes, AOI entered/left counts, and
  allocations against the pre-offload benchmark.

## Follow-Up Path

This phase creates the seam needed for the medium-sized design:

```text
Hub actor -> immutable replication jobs -> replicationDispatcher
```

The next phase can replace the actor-built `byRecipient` map with:

```text
Hub actor -> immutable World/AOI/Collectible delta -> ReplicationBuilder -> dispatcher
```

Only after replication building and AOI work remain saturated should the
project design spatial shard actors.
