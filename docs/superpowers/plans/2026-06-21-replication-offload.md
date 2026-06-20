# Replication Offload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move replication encoding and send-buffer enqueue out of the Hub actor
without changing server authority, replication semantics, or the WebSocket
protocol.

**Architecture:** Hub still owns `World`, `AOIIndex`, client membership, pending
replication buffers, and `byRecipient` construction. A new
`replicationDispatcher` accepts immutable per-recipient jobs and encodes/sends
them on deterministic recipient-partition workers. Send failure is fed back to
Hub through actor-owned disconnect handling.

**Tech Stack:** Go 1.26, existing `ClientSender`, existing
`TryEncodeReplicationUpdate`, existing `HubSnapshot`, existing realtime tests
and `BenchmarkHubReplication`.

**Required context:** Read `AGENTS.md`,
`docs/superpowers/specs/2026-06-21-replication-offload-design.md`,
`docs/superpowers/plans/2026-06-20-hub-actor-profiling.md`, and
`docs/concurrency-debugging.md` before implementation.

---

## Scope Guardrails

This plan does not introduce world shards, parallel AOI, parallel World
simulation, external queues, Redis, MySQL changes, protocol changes, browser
changes, or `byRecipient` construction outside the actor.

Keep `broadcastReplication` semantics intact. The only behavior change should
be that per-recipient encode/send work happens outside the Hub actor and send
failure is reported back asynchronously.

Task 2 through Task 5 touch Hub and replication hot paths. A single agent should
implement those tasks in order. Do not run another agent against `Hub.Run()` or
`broadcastReplication` concurrently.

---

### Task 1: Add Immutable Replication Job Helpers

- [ ] Deliver a small job type and copy helpers for crossing the actor/worker
  ownership boundary.

**Files:**

- Create: `internal/realtime/replication_dispatcher.go`
- Test: `internal/realtime/replication_dispatcher_test.go`

**Task boundary:**

- Define `replicationJob` with recipient ID, tick, client, and copied
  `ReplicationChanges`.
- Add a helper that deep-copies every slice field in `ReplicationChanges`.
- Do not add pooling in this task.
- Do not read Hub state from the helper.

**Behavioral goals:**

- Dispatcher workers never observe slices owned by `broadcastReplication`.
- The copy preserves every protocol field currently carried by
  `ReplicationChanges`.

**Verification:**

- Test that mutating the original `ReplicationChanges` after job creation does
  not change the job's copy.
- Test nil and empty slice behavior stays compatible with
  `TryEncodeReplicationUpdate`.
- Run `go test ./internal/realtime -run ReplicationJob`.

---

### Task 2: Implement Partitioned Replication Dispatcher

- [ ] Deliver a bounded worker dispatcher for encode/send work.

**Files:**

- Modify: `internal/realtime/replication_dispatcher.go`
- Modify: `internal/realtime/replication_dispatcher_test.go`

**Task boundary:**

- Implement a dispatcher with deterministic recipient partitioning:
  `recipientID % workerCount`.
- Give each worker a bounded channel.
- `Submit` must not block the Hub actor. If the target worker queue is full,
  return false and count the drop.
- Each worker must call `TryEncodeReplicationUpdate`.
- Empty updates must be skipped and counted.
- Encode errors must be counted and not crash the worker.
- Successful non-empty encodes must call `client.Send(data)`.
- Failed sends must be counted and reported through a callback with the
  recipient ID.
- `Stop` must stop accepting jobs, close worker queues, and wait for workers to
  exit.

**Behavioral goals:**

- Per-recipient ordering is preserved because every recipient maps to one
  worker.
- Dispatcher backpressure is bounded and visible.
- Encode/send no longer needs actor-owned state.

**Verification:**

- Test non-empty job sends bytes to the client.
- Test empty job does not send.
- Test send failure invokes the callback with the recipient ID.
- Test full queue returns false and increments drop stats without blocking.
- Test jobs for the same recipient are sent in submit order.
- Test `Stop` is idempotent enough for Hub shutdown tests.
- Run `go test ./internal/realtime -run ReplicationDispatcher`.

---

### Task 3: Add Dispatcher Stats

- [ ] Surface dispatcher counters through realtime stats.

**Files:**

- Modify: `internal/realtime/stats.go`
- Modify: `internal/realtime/hub.go`
- Modify: `internal/server/stats_test.go`
- Modify: `internal/realtime/hub_test.go`

**Task boundary:**

- Add dispatcher stats fields for submitted jobs, dropped jobs, encoded
  messages, skipped empty jobs, encode errors, send failures, queue depth,
  worker count, and encoded bytes.
- Include these fields in `HubSnapshot` or a nested struct under it.
- Publish the latest dispatcher counters on existing stats ticks.
- Do not add a separate API endpoint unless it is clearly smaller than
  extending the existing stats response.

**Behavioral goals:**

- Synthetic load and stats API consumers can see whether the bottleneck moved
  from actor tick time to dispatcher queue/worker pressure.
- Stats reads remain safe from any goroutine.

**Verification:**

- Update stats API tests to assert dispatcher stats serialize.
- Update Hub snapshot tests to assert dispatcher stats are present after a
  stats tick.
- Run `go test ./internal/realtime ./internal/server`.

---

### Task 4: Integrate Dispatcher Into Hub Lifecycle

- [ ] Construct, use, and stop the dispatcher with Hub.

**Files:**

- Modify: `internal/realtime/hub.go`
- Modify: `internal/realtime/manual_hub.go`
- Modify: `internal/realtime/hub_test.go`

**Task boundary:**

- Add a `replicationDispatcher` field to `Hub`.
- Construct a default dispatcher in `NewHubWithSavedPositions` / `newHub`.
- Use a small default worker count derived from `GOMAXPROCS` or a fixed
  conservative value.
- Use a bounded per-worker queue.
- On send failure callback, request Hub-owned disconnect handling; do not call
  `removeClient` from a dispatcher worker.
- Stop the dispatcher during Hub shutdown after no more broadcast jobs can be
  submitted.
- Keep direct-test helpers able to construct a Hub without starting `Run()`.

**Behavioral goals:**

- Hub owns dispatcher lifetime.
- Worker goroutines do not leak after `Hub.Stop()`.
- Send failure still results in client removal and final position save through
  actor-owned paths.

**Verification:**

- Add or update a Hub test that forces dispatcher send failure and observes
  eventual disconnect through actor handling.
- Add or update a shutdown test that confirms dispatcher workers stop.
- Run `go test ./internal/realtime`.

---

### Task 5: Offload broadcastReplication Encode/Send

- [ ] Replace the final encode/send loop in `broadcastReplication` with
  dispatcher submissions.

**Files:**

- Modify: `internal/realtime/hub.go`
- Modify: `internal/realtime/replication_benchmark_test.go`
- Modify: `internal/realtime/hub_test.go`

**Task boundary:**

- Keep all state-gathering and `byRecipient` accumulation inside
  `broadcastReplication`.
- In the final recipient loop, create immutable jobs and call dispatcher
  `Submit`.
- Count submitted jobs and dropped jobs through dispatcher stats.
- Keep replication message and byte accounting consistent with worker-side
  encode results.
- Do not block broadcast tick waiting for encode/send completion.

**Behavioral goals:**

- Hub actor exits broadcast tick earlier under high recipient counts.
- Existing clients still receive semantically identical `replication_update`
  messages.
- Benchmarks still report comparable message and byte counts after draining
  client buffers.

**Verification:**

- Run `go test ./internal/realtime`.
- Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- Confirm benchmark `msgs/op`, `bytes/op`, `entered/op`, and `left/op` remain
  logically comparable to the pre-offload baseline.

---

### Task 6: Add Direct Dispatcher Benchmarks

- [ ] Measure dispatcher encode/send throughput independently from Hub AOI work.

**Files:**

- Modify: `internal/realtime/replication_benchmark_test.go`

**Task boundary:**

- Add a benchmark that submits representative `ReplicationChanges` jobs to the
  dispatcher and drains test clients.
- Report jobs/op, bytes/op, dropped/op, and worker count.
- Keep the existing `BenchmarkHubReplication` benchmark.

**Behavioral goals:**

- Future optimization can distinguish dispatcher saturation from actor
  saturation.
- Worker count and queue sizing can be tuned with evidence.

**Verification:**

- Run `go test ./internal/realtime -run '^$' -bench 'Benchmark(HubReplication|ReplicationDispatcher)' -benchmem`.

---

### Task 7: Update Documentation

- [ ] Record the new replication offload phase.

**Files:**

- Modify: `docs/map-walker-handoff.md`
- Optional modify: `docs/hub-architecture-analysis.md`

**Task boundary:**

- Keep `docs/map-walker-handoff.md` concise.
- Record the dispatcher responsibility, stats fields, and known limits.
- Do not paste benchmark history into handoff.

**Behavioral goals:**

- Future agents know encoding/send is no longer actor-owned.
- The next phase can start from the documented seam:
  `Hub -> immutable replication jobs -> replicationDispatcher`.

**Verification:**

- Read `docs/map-walker-handoff.md` after editing and confirm it remains a
  compact current-state handoff.

---

## Final Verification

- [ ] Run `go test ./internal/realtime`.
- [ ] Run `go test ./internal/server`.
- [ ] Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- [ ] Run `go test ./internal/realtime -run '^$' -bench BenchmarkReplicationDispatcher -benchmem`.
- [ ] If feasible, run a synthetic-client smoke test and confirm broadcast tick
  rate improves or dispatcher queue pressure is visible in stats.

Do not claim capacity improvement without benchmark or synthetic-load output.
If throughput does not improve, keep the dispatcher stats and report whether the
bottleneck remains in `byRecipient`, AOI work, or another actor-owned phase.
