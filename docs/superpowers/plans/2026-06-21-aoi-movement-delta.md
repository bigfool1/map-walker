# AOI Movement Delta Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add AOI movement deltas that expose entered, left, and stable
neighbors from one AOI movement update, then use those deltas to remove
`snapshotMoverVisibility` and repeated stable-neighbor `VisibleNeighbors`
lookups.

**Architecture:** `internal/game/AOIIndex` remains pure logic and owns AOI
relationship mutation. `internal/realtime/Hub` remains the actor that gathers
movement, filters connected clients, and submits replication jobs.
`ReplicationBuilder` consumes movement deltas for stable position fanout instead
of old-neighbor snapshots plus new `VisibleNeighbors` reads.

**Tech Stack:** Go 1.26, existing `AOIIndex`, existing Hub actor, existing
`ReplicationBuilder`, existing `BenchmarkHubReplication`.

**Required context:** Read `AGENTS.md`,
`docs/superpowers/specs/2026-06-21-aoi-movement-delta-design.md`,
`docs/superpowers/specs/2026-06-21-replication-builder-design.md`,
`docs/benchmarks/replication-builder.md`, and
`docs/concurrency-debugging.md` before implementation.

---

## Scope Guardrails

This plan does not change AOI geometry, 500m enter / 600m leave hysteresis,
symmetric relationship semantics, cell size, nine-cell candidate coverage,
WebSocket protocol fields, collectible grid algorithms, spatial sharding, or
AOI parallelism.

This plan is movement-delta plumbing. It does not implement boundary-aware
incremental AOI skipping.

Task 1 and Task 2 are pure `internal/game/` work and may be done independently.
Task 3 through Task 6 touch `internal/realtime/Hub` and
`ReplicationBuilder`; use a single agent for those tasks in order. Do not run
another agent against `Hub.Run()` or `broadcastReplication` concurrently.

---

### Task 1: Add AOI MovementDelta Type And API

- [ ] Add a detailed AOI movement API without changing existing `Move`
  behavior.

**Files:**

- Modify: `internal/game/aoi.go`
- Modify: `internal/game/aoi_test.go`

**Task boundary:**

- Add `MovementDelta` with `PlayerID`, `Entered`, `Left`, and `Stable`.
- Add `MoveDetailed(playerID int64, lat, lng float64) MovementDelta`.
- Keep `Move(playerID, lat, lng)` returning `RelationshipChanges`.
- Implement `Move` as a wrapper around `MoveDetailed` if that is the smallest
  safe change.
- Return an empty delta for unknown players.
- Preserve all existing relationship semantics and stats counters.
- Do not change `Insert`, `Remove`, `RecalculateRelationships`, cell size, or
  distance thresholds.

**Behavioral goals:**

- `MoveDetailed.Entered` and `MoveDetailed.Left` match existing `Move`.
- `MoveDetailed.Stable` contains neighbors visible before and after movement.
- AOI collection order remains unspecified.

**Verification:**

- Test unknown player returns empty delta.
- Test entered and left match existing `Move` in deterministic scenarios.
- Test stable contains old-and-still-visible neighbors.
- Test stable excludes newly entered neighbors.
- Test stable excludes left neighbors.
- Run `go test ./internal/game`.

---

### Task 2: Remove Nested Stable Snapshot Need From AOI Tests

- [ ] Add focused AOI tests for stable neighbor semantics and hysteresis.

**Files:**

- Modify: `internal/game/aoi_test.go`

**Task boundary:**

- Add tests that use set equality for `Entered`, `Left`, and `Stable`.
- Cover no-cell-change movement where neighbors remain stable.
- Cover enter-radius movement where a new neighbor enters and is not stable.
- Cover leave-radius movement where an old neighbor leaves and is not stable.
- Keep existing AOI order-insensitive behavior.

**Behavioral goals:**

- Future AOI optimizations can rely on explicit stable-neighbor contract.
- Existing hysteresis and symmetry tests still protect geometry behavior.

**Verification:**

- Run `go test ./internal/game`.
- Run `go test ./internal/game -count=20`.

---

### Task 3: Change ReplicationBuilder Input To Movement Deltas

- [ ] Make `ReplicationBuilder` consume movement deltas for stable position
  fanout.

**Files:**

- Modify: `internal/realtime/replication_builder.go`
- Modify: `internal/realtime/replication_builder_test.go`

**Task boundary:**

- Replace `MovedIDs` plus `OldNeighborsByMover` input with movement deltas.
- Keep `PendingEntered`, `PendingLeft`, `PendingAppearances`, and collectible
  pending inputs unless moving entered/left into deltas is strictly smaller.
- For each movement delta:
  - send self position to the mover if connected
  - send position to connected stable neighbors
- Do not call `VisibleNeighbors` for stable position fanout.
- Keep protocol normalization in `TryEncodeReplicationUpdate`; do not duplicate
  normalization in builder.

**Behavioral goals:**

- Builder no longer needs old-neighbor snapshots or new-neighbor reads to
  compute stable mover position recipients.
- Player replication behavior remains equivalent.

**Verification:**

- Update builder tests for moved self-position fanout.
- Update stable-neighbor position fanout tests to use movement deltas.
- Test entered neighbor does not receive duplicate stable position update for
  the same movement.
- Test left neighbor receives no stable position update for the same movement.
- Run `go test ./internal/realtime -run ReplicationBuilder`.

---

### Task 4: Replace Hub Snapshot Path With Movement Deltas

- [ ] Remove `snapshotMoverVisibility` from `broadcastReplication`.

**Files:**

- Modify: `internal/realtime/hub.go`
- Modify: `internal/realtime/hub_test.go`

**Task boundary:**

- Replace:
  - `snapshotMoverVisibility(movedIDs)`
  - `applyMovementAOIChanges(movedIDs)`
  - builder input with old-neighbor snapshot
- With:
  - `applyMovementAOIDeltas(movedIDs)` returning movement deltas
  - builder input with movement deltas
- `applyMovementAOIDeltas` remains actor-owned and still reads from `World`,
  calls AOI movement, reads player state for entered events, filters connected
  clients, and fills pending entered/left buffers.
- Remove `snapshotMoverVisibility` and `moverHadNeighbor` only if no remaining
  production or test code uses them.

**Behavioral goals:**

- Hub no longer copies old visible neighbors into
  `map[int64]map[int64]struct{}`.
- Hub no longer requires builder to call `VisibleNeighbors` for stable movement
  fanout.
- Existing client-visible replication behavior remains equivalent.

**Verification:**

- Run `go test ./internal/realtime`.
- Run any existing tests that cover movement, enter, leave, and slow-client AOI
  behavior with `-count=20`.

---

### Task 5: Add AOI Movement Delta Metrics

- [ ] Measure movement-delta cost separately from builder and collectible
  visibility.

**Files:**

- Modify: `internal/realtime/stats.go`
- Modify: `internal/realtime/hub.go`
- Modify: `internal/server/stats_test.go`
- Modify: `internal/realtime/hub_test.go`

**Task boundary:**

- Add or extend stats to capture:
  - AOI detailed movement duration
  - stable delta construction duration if measured separately
  - collectible visibility recalculation duration
  - collectible replacement fanout duration
- Keep existing AOI candidate pair, distance check, entered, and left counters.
- Keep builder and dispatcher stats separate.

**Behavioral goals:**

- Reports can tell whether movement delta plumbing reduced snapshot/stable
  fanout cost and whether remaining cost moved to collectible visibility or
  AOI recalculation.

**Verification:**

- Update stats API tests to assert new timing fields serialize.
- Update Hub snapshot tests to assert timings are present after a stats tick.
- Run `go test ./internal/realtime ./internal/server`.

---

### Task 6: Update Benchmarks And Evidence Report

- [ ] Compare movement-delta path against the replication-builder baseline.

**Files:**

- Modify: `internal/realtime/replication_benchmark_test.go`
- Create or modify: `docs/benchmarks/aoi-movement-delta.md`
- Modify: `docs/map-walker-handoff.md`

**Task boundary:**

- Keep `BenchmarkHubReplication`.
- Add benchmark reporting for movement-delta path if needed.
- Compare before/after:
  - `BenchmarkHubReplication` ns/op
  - AOI candidate pairs
  - AOI distance checks
  - AOI relationships entered/left
  - replication messages
  - replication bytes
  - allocations
- Record whether `snapshotMoverVisibility` was removed from production path.
- Keep `docs/map-walker-handoff.md` concise and current-state focused.

**Decision rule:**

- If AOI movement time drops and remaining cost is collectible visibility, plan
  collectible visibility optimization next.
- If AOI movement still dominates and candidate/distance counts remain high,
  plan boundary-aware incremental AOI movement next.
- If relationship map lookups dominate, plan dense relationship storage or
  integer-indexed player IDs next.
- If builder or immutable copy costs become dominant, return to replication
  data-layout optimization.

**Verification:**

- Run `go test ./internal/realtime`.
- Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- Run `go test ./internal/game`.
- If available, run a synthetic-client smoke profile and record whether
  broadcast tick rate changed.

---

## Final Verification

- [ ] Run `go test ./internal/game`.
- [ ] Run `go test ./internal/realtime`.
- [ ] Run `go test ./internal/server`.
- [ ] Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- [ ] Confirm AOI entered/left counts and replication message/byte counts remain
  logically comparable to the replication-builder baseline.
- [ ] Confirm `snapshotMoverVisibility` is no longer in the production
  `broadcastReplication` path.
- [ ] Record the next optimization recommendation in
  `docs/benchmarks/aoi-movement-delta.md`.

Do not claim an AOI algorithm improvement beyond the measured delta-plumbing
effect. If the benchmark does not improve, keep the explicit movement-delta seam
and use the report to decide whether true incremental AOI, collectible
visibility, or relationship storage should come next.
