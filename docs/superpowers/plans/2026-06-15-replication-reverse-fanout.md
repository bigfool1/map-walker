# Replication Reverse Fan-Out Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Track each
> task with its checkbox.

**Goal:** Replace the Hub replication all-client-by-all-mover scan with
recipient-oriented reverse fan-out while preserving current protocol behavior
and demonstrating lower broadcast Tick duration at 2,000 and 3,000 clients.

**Architecture:** Capture old visibility only for moved players, apply the
existing AOI movement and pending-event logic, then accumulate
`ReplicationChanges` by recipient. Keep normalization, JSON encoding, bounded
send queues, and queue-full removal at their existing boundaries.

**Tech Stack:** Go 1.26, existing World/AOI/Hub actor, Go benchmarks, Go tests,
Go vet.

**Required context:** Read
`docs/superpowers/specs/2026-06-15-replication-reverse-fanout-design.md`,
`AGENTS.md`, and `docs/concurrency-debugging.md` before implementation.

---

### Task 1: Freeze Current Multi-Mover Replication Semantics

- [ ] Add regression tests that capture current logical wire behavior before
  replacing the algorithm.

**Task boundary:**

- Add focused Hub tests for multiple stable movers updating one observer.
- Add same-broadcast multiple-entry and multiple-leave scenarios.
- Cover entry plus position, leave plus appearance, and self-position conflict
  suppression.
- Cover queue-full removal while other accumulated recipients still receive
  valid updates.
- Preserve current movement-induced entry and leave directionality; do not
  redefine the protocol.
- Do not modify production replication code in this task.

**Behavioral goals:**

- Stable visible movers produce one current position each for the observer.
- Entered players do not also appear in `positions`.
- Left players do not also appear in `positions` or `appearances`.
- Each moved client receives at most one `selfPosition`.
- A failed recipient does not prevent later recipients from receiving their
  updates.
- Logical message content remains deterministic after normalization.

**Affected modules:**

- Modify `internal/realtime/hub_test.go`
- Modify `internal/realtime/aoi_scale_test.go` only if the existing scale
  scenario needs an additional invariant

**Verification:**

- Run `go test ./internal/realtime`.
- Repeat the focused multi-mover tests with `-count=20`.
- Confirm the new tests pass against the current implementation.

---

### Task 2: Add Deterministic Hub Replication Benchmarks And Record Baseline

- [ ] Add 2,000- and 3,000-client benchmarks for the current broadcast path and
  record pre-change results.

**Task boundary:**

- Add a benchmark that exercises World movement, AOI movement, recipient-
  specific assembly, JSON encoding, and bounded immediately drained send
  queues.
- Use deterministic placement matching the synthetic 10km by 10km activity
  region, fixed IDs, a fixed movement ratio, and a fixed movement pattern.
- Report standard benchmark duration and allocation metrics.
- Report moved players, AOI relationship changes, replication messages, and
  replication bytes for logical-equivalence comparison.
- Keep HTTP, WebSocket framing, kernel buffers, persistence, browser parsing,
  and frontend rendering out of this benchmark.
- Record machine, Go version, command, scenario parameters, and repeated
  baseline results.

**Behavioral goals:**

- Repeated runs use identical logical workloads.
- The benchmark consumes client sends so queue fullness does not become the
  measured bottleneck.
- Message and byte totals are stable across repeats.
- The baseline is sufficient to compare the old and new replication
  algorithms without relying on service-level timing.

**Affected modules:**

- Create `internal/realtime/replication_benchmark_test.go`
- Create `docs/benchmarks/replication-reverse-fanout.md`
- Reuse `internal/synthetic/placement.go` behavior only through an appropriate
  shared or benchmark-local deterministic fixture; do not make realtime depend
  on the synthetic package

**Verification:**

- Run `go test ./internal/realtime`.
- Run `go test -run '^$' -bench 'BenchmarkHubReplication/(2000|3000)$' -benchmem -count=5 ./internal/realtime`.
- Confirm each scale completes repeatedly with stable logical counters.
- Save the pre-change measurements before modifying `broadcastReplication`.

---

### Task 3: Replace Full Visibility Snapshot With Mover-Local Snapshots

- [ ] Capture old visibility only for moved players and remove the all-client
  visibility snapshot.

**Task boundary:**

- Build old-neighbor sets keyed only by moved player ID before applying AOI
  movement.
- Continue applying AOI changes through the existing
  `applyMovementAOIChanges` path.
- Remove `snapshotVisibility` and `wasVisibleTo` when they become unused.
- Do not change AOI geometry, hysteresis, relationship symmetry, or pending
  event generation.
- Keep all Hub state access inside the actor loop.

**Behavioral goals:**

- A mover's old visibility is captured at the broadcast boundary before any
  mover AOI update.
- Final visibility is read after all mover AOI updates.
- Stable, entered, and left relationships can be distinguished without
  copying every connected client's visible set.
- Existing AOI stats and pending buffers retain their current meaning.

**Affected modules:**

- Modify `internal/realtime/hub.go`
- Modify `internal/realtime/hub_test.go` only for focused internal-boundary
  coverage if required

**Verification:**

- Run `go test ./internal/realtime`.
- Repeat AOI movement, hysteresis, and multi-mover tests with `-count=20`.
- Confirm no new synchronization or locks are introduced.

---

### Task 4: Accumulate Replication Changes By Recipient

- [ ] Replace the connected-client-by-moved-player scan with reverse fan-out
  into recipient accumulators.

**Task boundary:**

- Create a broadcast-local accumulator keyed by recipient ID.
- Accumulate mover self positions for connected movers.
- Fan stable mover positions out to final visible neighbors that also existed
  in the mover's old-neighbor set.
- Fan pending entered states out to their final visible connected neighbors.
- Copy pending leave IDs into their existing recipient-keyed destinations.
- Fan pending appearances out to the changed player and current visible
  connected neighbors.
- Encode and send only for accumulated, still-connected recipients.
- Keep `NormalizeReplicationChanges`, `TryEncodeReplicationUpdate`, stats
  counters, send-buffer behavior, and queue-full removal unchanged.
- Do not introduce persistent accumulator state, pools, worker goroutines, or
  protocol changes.

**Behavioral goals:**

- Eliminate the `connected clients × moved players` loop.
- Preserve current logical message content, conflict suppression, and stable
  wire ordering.
- Skip clients with no accumulated changes.
- Safely continue iteration when a send failure removes a client.
- Consume pending entered, left, and appearance buffers exactly once per
  broadcast.

**Affected modules:**

- Modify `internal/realtime/hub.go`
- Modify `internal/realtime/hub_test.go`
- Modify `internal/realtime/aoi_scale_test.go` only for logical metrics or
  invariants required by the new path

**Verification:**

- Run `go test ./internal/realtime`.
- Run focused regression tests with `-count=20`.
- Run `go test -race ./internal/realtime`.
- Confirm replication message and byte totals match the frozen benchmark
  workload.

---

### Task 5: Compare Performance And Document The Result

- [ ] Run the optimized benchmarks, compare them with baseline, and document
  measured gains and remaining bottlenecks.

**Task boundary:**

- Run the same 2,000- and 3,000-client benchmark commands and repetition count
  used for baseline.
- Compare `ns/op`, `B/op`, `allocs/op`, replication messages, replication
  bytes, moved players, and relationship changes.
- Investigate any logical-counter mismatch before accepting performance
  results.
- Record results without claiming restored service-level 20 Hz/10 Hz capacity.
- Update the project handoff with the new replication data flow and measured
  boundary.
- Do not add unrelated encoding, persistence, queue, transport, or frontend
  optimizations in this task.

**Behavioral goals:**

- Logical counters and protocol behavior remain unchanged for the frozen
  workload.
- Broadcast duration clearly declines at both 2,000 and 3,000 clients.
- Allocation changes are reported whether positive, neutral, or negative.
- Remaining bottlenecks are described as follow-up evidence, not included in
  this implementation.

**Affected modules:**

- Modify `docs/benchmarks/replication-reverse-fanout.md`
- Modify `docs/map-walker-handoff.md`
- Modify benchmark or realtime tests only to correct defects found during
  verification

**Verification:**

- Run `go test -run '^$' -bench 'BenchmarkHubReplication/(2000|3000)$' -benchmem -count=5 ./internal/realtime`.
- Run `go test ./...`.
- Run `go test -race ./internal/realtime`.
- Run `go vet ./...`.
- Run `git diff --check`.
- Confirm the benchmark report contains the baseline, optimized result,
  environment, exact commands, logical counters, and conclusion.
