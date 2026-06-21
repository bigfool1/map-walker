# Replication Builder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract per-recipient replication fanout from
`Hub.broadcastReplication` into a synchronous `ReplicationBuilder`, then profile
whether the next bottleneck is AOI/fanout algorithms, immutable job copying,
dispatcher pressure, or a future async builder seam.

**Architecture:** Hub remains the only owner of `World`, `AOIIndex`, connected
clients, pending replication buffers, and actor state mutation. The builder
runs synchronously on the Hub actor goroutine through a reader interface valid
only during `Build`. The builder returns immutable dispatcher jobs but does not
encode, send, mutate Hub state, or submit to the dispatcher.

**Tech Stack:** Go 1.26, existing `ReplicationDispatcher`, existing
`replicationJob`, existing `copyReplicationChanges`, existing realtime tests,
existing `HubSnapshot`, and `BenchmarkHubReplication`.

**Required context:** Read `AGENTS.md`,
`docs/superpowers/specs/2026-06-21-replication-builder-design.md`,
`docs/superpowers/specs/2026-06-21-replication-offload-design.md`,
`docs/superpowers/plans/2026-06-21-replication-offload.md`, and
`docs/concurrency-debugging.md` before implementation.

---

## Scope Guardrails

This plan does not introduce spatial shards, parallel AOI, parallel World
simulation, async builder workers, builder queues, snapshot readers, Redis,
MySQL changes, WebSocket protocol changes, or browser changes.

This plan does not claim direct actor CPU reduction from the synchronous
builder. It improves locality and creates a precise profile seam. The next
optimization must be chosen from metrics after this plan is complete.

Tasks touch `Hub.broadcastReplication`; use a single agent for the Hub-facing
tasks. Do not run another agent against `Hub.Run()` or `broadcastReplication`
concurrently.

---

### Task 1: Define Builder Input And Reader

- [ ] Add the types that describe synchronous builder inputs.

**Files:**

- Create: `internal/realtime/replication_builder.go`
- Test: `internal/realtime/replication_builder_test.go`

**Task boundary:**

- Define `ReplicationBuildInput` with tick, moved IDs, old-neighbor snapshot,
  pending player changes, pending appearance changes, and pending collectible
  changes.
- Define `ReplicationBuildReader` with `Connected`, `Client`,
  `VisibleNeighbors`, and `PlayerPosition`.
- Define a Hub reader adapter that wraps the current Hub fields but is only used
  synchronously inside `broadcastReplication`.
- Define a concrete reader variant for benchmark comparison. It should hold the
  current `*game.AOIIndex`, connected client map, and required player-position
  access without going through an interface call at each hot-path lookup.
- Do not let the builder store the reader.
- Do not add a snapshot reader in this task.
- Do not move AOI mutation or pending-buffer draining into the builder.

**Behavioral goals:**

- The builder interface makes actor-owned data dependencies explicit.
- The first seam avoids copying full visibility snapshots.

**Verification:**

- Add compile-time assertions or focused tests for the Hub reader adapter.
- Add focused tests that the interface reader and concrete reader return the
  same connected/client/neighbor/position answers in a deterministic fixture.
- Run `go test ./internal/realtime -run ReplicationBuilder`.

---

### Task 2: Move Player Fanout Into Builder

- [ ] Move moved/self/entered/left/appearance fanout from Hub into builder.

**Files:**

- Modify: `internal/realtime/replication_builder.go`
- Modify: `internal/realtime/replication_builder_test.go`
- Modify: `internal/realtime/hub.go`

**Task boundary:**

- Builder accumulates `ReplicationChanges` through a local `byRecipient` map.
- Move self-position fanout for moved connected players.
- Move stable-neighbor position fanout using `oldNeighborsByMover` and
  `VisibleNeighbors`.
- Move entered player fanout.
- Move left player fanout.
- Move appearance fanout to self and visible neighbors.
- Keep existing normalization behavior in `TryEncodeReplicationUpdate`; do not
  duplicate protocol normalization in the builder.

**Behavioral goals:**

- Player replication behavior remains equivalent.
- `broadcastReplication` no longer owns player fanout details.

**Verification:**

- Test moved self-position fanout.
- Test stable-neighbor position fanout only goes to old-and-still-visible
  connected neighbors.
- Test entered player fanout goes to connected visible neighbors.
- Test left player fanout follows recipient-keyed pending-left input.
- Test appearance fanout goes to self and connected visible neighbors.
- Run `go test ./internal/realtime`.

---

### Task 3: Move Collectible Fanout Into Builder

- [ ] Move collectible recipient accumulation from Hub into builder.

**Files:**

- Modify: `internal/realtime/replication_builder.go`
- Modify: `internal/realtime/replication_builder_test.go`
- Modify: `internal/realtime/hub.go`

**Task boundary:**

- Move collectible entered, left, spawned, and collected fanout.
- Preserve recipient-keyed semantics for collectible pending maps.
- Keep collectible visibility recalculation and replacement advancement in Hub.

**Behavioral goals:**

- Builder owns all per-recipient `ReplicationChanges` accumulation.
- Hub only gathers pending collectible changes and passes them into the builder.

**Verification:**

- Test collectible entered fanout.
- Test collectible left fanout.
- Test collectible spawned fanout.
- Test collectible collected fanout.
- Run `go test ./internal/realtime`.

---

### Task 4: Return Immutable Jobs From Builder

- [ ] Have builder return dispatcher-ready jobs.

**Files:**

- Modify: `internal/realtime/replication_builder.go`
- Modify: `internal/realtime/replication_builder_test.go`
- Modify: `internal/realtime/hub.go`

**Task boundary:**

- Builder converts `byRecipient` entries to `[]replicationJob`.
- Use the existing `copyReplicationChanges` helper before jobs leave the
  builder.
- Skip disconnected recipients through the reader.
- Preserve the post-dispatcher counter split:
  - `ReplicationRecipients` is Hub-side builder-produced recipient jobs
    submitted to dispatcher.
  - `ReplicationMessages` is dispatcher encoded non-empty messages.
  - `ReplicationBytes` is dispatcher encoded bytes.
- Do not submit to dispatcher from inside builder.

**Behavioral goals:**

- Builder is pure fanout construction.
- Dispatcher remains responsible for encode/send.
- Hub remains responsible for submitting jobs and actor lifecycle.

**Verification:**

- Test builder returns no jobs for empty input.
- Test returned jobs are immutable if the original input slices are mutated
  after `Build`.
- Test disconnected recipients do not receive jobs.
- Run `go test ./internal/realtime`.

---

### Task 5: Add Builder Metrics

- [ ] Surface builder fanout and immutable-copy cost separately from dispatcher
  metrics.

**Files:**

- Modify: `internal/realtime/replication_builder.go`
- Modify: `internal/realtime/stats.go`
- Modify: `internal/realtime/hub.go`
- Modify: `internal/server/stats_test.go`
- Modify: `internal/realtime/hub_test.go`

**Task boundary:**

- Add builder stats for build calls, jobs produced, recipients touched, changes
  appended, by-recipient accumulation duration, immutable-copy duration, and
  total build duration.
- Include builder stats in `HubSnapshot` or a nested struct under it.
- Keep dispatcher stats separate.
- Do not add snapshot build metrics because snapshots are out of scope for this
  plan.
- Keep replication counter names documented in stats tests so future changes do
  not blur submitted-recipient count with encoded-message count.

**Behavioral goals:**

- Stats distinguish fanout construction from dispatcher encode/send pressure.
- Stats expose immutable job construction cost before async builder is
  considered.

**Verification:**

- Update stats API tests to assert builder stats serialize.
- Update Hub snapshot tests to assert builder stats are present after a stats
  tick.
- Update stats tests to assert `ReplicationRecipients` and
  `Dispatcher.Encoded` are distinct fields with distinct semantics.
- Run `go test ./internal/realtime ./internal/server`.

---

### Task 6: Simplify broadcastReplication Orchestration

- [ ] Replace in-Hub fanout construction with builder invocation.

**Files:**

- Modify: `internal/realtime/hub.go`
- Modify: `internal/realtime/hub_test.go`
- Modify: `internal/realtime/replication_benchmark_test.go`

**Task boundary:**

- `broadcastReplication` still performs actor-owned gather steps.
- Create `ReplicationBuildInput` from gathered values.
- Call the synchronous builder with the Hub reader.
- Submit returned jobs to the existing dispatcher.
- Keep the early return when there is no movement or pending change.
- Remove now-unused Hub-local `getOrCreateRecipient` if it is no longer used.

**Behavioral goals:**

- `broadcastReplication` reads as orchestration rather than fanout
  implementation.
- Existing benchmark message and byte counts remain logically comparable.

**Verification:**

- Run `go test ./internal/realtime`.
- Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- Compare `msgs/op`, `bytes/op`, `entered/op`, and `left/op` with the
  dispatcher baseline.

---

### Task 7: Add Direct Builder Benchmarks

- [ ] Measure builder fanout cost independently from AOI mutation and
  dispatcher encoding.

**Files:**

- Modify: `internal/realtime/replication_benchmark_test.go`

**Task boundary:**

- Add a direct benchmark for `ReplicationBuilder.Build`.
- Add two direct benchmark variants:
  - interface reader
  - concrete reader
- Use deterministic input with moved players, stable neighbors, appearances,
  and collectible changes.
- Report jobs/op, recipients/op, changes/op, by-recipient accumulation duration,
  immutable-copy duration, total build duration, and allocations.
- Keep existing `BenchmarkHubReplication` and dispatcher benchmarks.
- If the interface reader shows measurable `ns/op` or `allocs/op` regression
  versus the concrete reader, switch the production builder to the concrete
  reader or explicit function fields before completing this plan.

**Behavioral goals:**

- Future work can tell whether time is in builder fanout, immutable job copies,
  AOI work, or dispatcher encode/send.
- The reader abstraction does not quietly add avoidable overhead to the actor
  hot path.

**Verification:**

- Run `go test ./internal/realtime -run '^$' -bench 'Benchmark(HubReplication|ReplicationBuilder|ReplicationDispatcher)' -benchmem`.

---

### Task 8: Profile And Choose Next Optimization

- [ ] Produce a short evidence note after Phase A implementation.

**Files:**

- Create or modify: `docs/benchmarks/replication-builder.md`
- Modify: `docs/map-walker-handoff.md`

**Task boundary:**

- Record benchmark commands and headline results.
- Compare actor-side broadcast cost before and after builder extraction.
- Classify the next bottleneck as AOI/collectible visibility, builder fanout,
  immutable job copying, dispatcher pressure, or inconclusive.
- Record interface-reader versus concrete-reader benchmark results and the
  chosen production reader shape.
- Record the counter semantics:
  - `ReplicationRecipients` = builder-produced jobs submitted to dispatcher
  - `ReplicationMessages` = dispatcher encoded messages
  - `ReplicationBytes` = dispatcher encoded bytes
- Keep `docs/map-walker-handoff.md` concise and current-state focused.

**Decision rule:**

- If time is mostly in AOI, `VisibleNeighbors`, or collectible visibility, plan
  AOI/fanout algorithm optimization next.
- If time is mostly in `ReplicationBuilder.Build`, consider a separate
  snapshot-and-async-builder spec.
- If time is mostly in `copyReplicationChanges` or immutable job construction,
  plan data-layout and copy optimization next.
- If dispatcher queue depth or dropped jobs rise, plan dispatcher worker,
  queue, or backpressure tuning next.

**Verification:**

- Run `go test ./internal/realtime`.
- Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- Run `go test ./internal/realtime -run '^$' -bench BenchmarkReplicationBuilder -benchmem`.
- If available, run a synthetic-client smoke profile and record whether
  broadcast tick rate changed.

---

## Final Verification

- [ ] Run `go test ./internal/realtime`.
- [ ] Run `go test ./internal/server`.
- [ ] Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- [ ] Run `go test ./internal/realtime -run '^$' -bench BenchmarkReplicationBuilder -benchmem`.
- [ ] Compare Hub replication benchmark counts against the dispatcher baseline:
  messages, bytes, AOI entered, AOI left, and allocations.
- [ ] Record the next optimization recommendation in
  `docs/benchmarks/replication-builder.md`.

Do not claim capacity improvement from synchronous builder extraction unless
benchmark or synthetic-load output shows it. This phase primarily improves
locality and creates the profile seam needed to choose the next optimization.
