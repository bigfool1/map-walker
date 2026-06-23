# AOI Enter Scan Observability And Workload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add skip-rate observability and continuous-movement benchmark coverage
so AOI enter-scan fast-path effectiveness can be measured online and in
benchmarks.

**Architecture:** Keep AOI algorithm behavior unchanged. `AOIIndex` continues
to increment raw counters during movement. Hub publishes one-second immutable
stats snapshots, and HTTP stats handlers only serialize snapshots. Benchmarks
split worst-case random-jump workload from continuous movement workload.

**Tech Stack:** Go 1.26, existing `AOIStats`, existing `HubSnapshot`, existing
`/api/stats/synthetic`, existing `BenchmarkHubReplication`.

**Required context:** Read `AGENTS.md`,
`docs/superpowers/specs/2026-06-23-aoi-enter-scan-observability-workload-design.md`,
`docs/superpowers/specs/2026-06-21-aoi-incremental-enter-scan-design.md`, and
`docs/concurrency-debugging.md` before implementation.

---

## Scope Guardrails

This plan does not change AOI enter-scan behavior, `EnterRescanDistanceMeters`,
movement speed, AOI geometry, collectible visibility, WebSocket protocol, or
the synthetic client manager.

Do not add per-player stats, per-cell stats, or HTTP-time AOI traversal.
Stats must remain raw counters collected on the hot path and published through
Hub snapshots.

Benchmark work may be done independently from stats API work. Any changes to
`internal/realtime/hub.go` should be done by one agent at a time.

---

### Task 1: Add Skip Rate To Hub Snapshot

- [ ] Expose `enter_scan_skip_rate` from the existing stats snapshot.

**Files:**

- Modify: `internal/realtime/stats.go`
- Modify: `internal/realtime/hub.go`
- Modify: `internal/server/stats_test.go`
- Modify: `internal/realtime/hub_test.go`

**Task boundary:**

- Keep raw counters:
  - `AOIFullEnterScans`
  - `AOISkippedEnterScans`
  - `AOILeaveChecks`
  - `AOIStableRelationships`
- Add a derived skip-rate field to `HubSnapshot`, or document and test the
  field name if one already exists.
- Compute skip rate only when publishing the Hub snapshot in `logStats`.
- If `full + skipped == 0`, represent skip rate as `0`.
- Do not calculate skip rate inside HTTP handlers.
- Do not traverse AOI maps to calculate stats.

**Behavioral goals:**

- `/api/stats/synthetic` can report fast-path eligibility without live AOI
  traversal.
- Stats pressure remains equivalent to existing AOI counters.

**Verification:**

- Update stats API tests to assert raw counters and skip rate serialize.
- Add or update Hub snapshot tests for denominator-zero behavior.
- Run `go test ./internal/realtime ./internal/server`.

---

### Task 2: Label Existing HubReplication Benchmark As Worst Case

- [ ] Keep the existing workload but make its meaning explicit.

**Files:**

- Modify: `internal/realtime/replication_benchmark_test.go`

**Task boundary:**

- Preserve the current benchmark behavior as the random-jump or worst-case
  workload.
- Rename or add a parent benchmark label such as
  `BenchmarkHubReplicationRandomJump`.
- Keep existing client counts.
- Keep existing metrics.
- Add reports for:
  - `full_enter_scans/op`
  - `skipped_enter_scans/op`
  - `enter_scan_skip_rate`
  - `leave_checks/op`
  - `candidate_pairs/op`
  - `distance_checks/op`

**Behavioral goals:**

- Worst-case benchmark remains available and is no longer mistaken for
  continuous-player movement.

**Verification:**

- Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplicationRandomJump -benchmem`.
- Confirm skip rate is expected to be low or zero for this workload.

---

### Task 3: Add Continuous Movement HubReplication Benchmark

- [ ] Add a benchmark that models repeated directional movement rather than
  random jumps.

**Files:**

- Modify: `internal/realtime/replication_benchmark_test.go`

**Task boundary:**

- Add `BenchmarkHubReplicationContinuousMove`.
- Use deterministic initial placement.
- Keep players in the same `World` and `AOIIndex` across iterations.
- Apply directional input over repeated simulation ticks.
- Avoid resetting or jumping positions between iterations.
- Use movement distance implied by configured speed and tick interval.
- Warm up enough ticks to establish AOI relationships.
- Report the same metrics as random-jump benchmark:
  - `full_enter_scans/op`
  - `skipped_enter_scans/op`
  - `enter_scan_skip_rate`
  - `leave_checks/op`
  - `candidate_pairs/op`
  - `distance_checks/op`
  - `aoi_move_us/op`
  - `msgs/op`
  - `bytes/op`
  - `moved/op`
  - `entered/op`
  - `left/op`

**Behavioral goals:**

- Benchmark coverage includes the movement pattern expected from real players.
- Fast-path effectiveness can be measured without relying on production only.

**Verification:**

- Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplicationContinuousMove -benchmem`.
- Confirm continuous benchmark reports a nonzero skip rate when movement stays
  below the rescan threshold.

---

### Task 4: Add Benchmark Evidence Report

- [ ] Record benchmark commands and interpretation.

**Files:**

- Create or modify: `docs/benchmarks/aoi-enter-scan-observability.md`
- Modify: `docs/map-walker-handoff.md`

**Task boundary:**

- Record commands for random-jump and continuous-movement benchmarks.
- Record headline results for both workloads.
- Include full/skipped scans, skip rate, candidate pairs, distance checks, AOI
  movement time, messages, bytes, and allocations.
- Explain that random-jump is worst-case and continuous movement is closer to
  real player input.
- Keep `docs/map-walker-handoff.md` concise and current-state focused.

**Behavioral goals:**

- Future optimization decisions can compare online skip rate with both
  benchmark workloads.

**Verification:**

- Read the benchmark report and confirm it states which workload each result
  represents.
- Confirm the handoff remains compact.

---

### Task 5: Document Online Tick Interpretation

- [ ] Add operational guidance for interpreting 19-21 simulation ticks per
  second.

**Files:**

- Modify: `docs/benchmarks/aoi-enter-scan-observability.md`
- Modify: `docs/map-walker-handoff.md`

**Task boundary:**

- Document that 20 simulation ticks per second is the target.
- Document that 19-21 ticks per second is near target with jitter, not by
  itself proof of overload.
- Document warning combinations:
  - sustained `simulation_ticks < 19`
  - broadcast cadence below 10 Hz
  - rising actor handoff latency
  - rising dispatcher queue depth or drops
  - rising AOI detailed move duration
  - low enter-scan skip rate under continuous movement
  - WebSocket send failures
  - GC pauses or goroutine buildup
- Do not add new runtime behavior in this task.

**Behavioral goals:**

- Operators and future agents interpret tick jitter consistently.

**Verification:**

- Read the docs and confirm they distinguish "near budget" from "capacity
  warning".

---

## Final Verification

- [ ] Run `go test ./internal/realtime`.
- [ ] Run `go test ./internal/server`.
- [ ] Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplicationRandomJump -benchmem`.
- [ ] Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplicationContinuousMove -benchmem`.
- [ ] Confirm `/api/stats/synthetic` serializes raw enter-scan counters and
  skip rate from a snapshot.
- [ ] Confirm no HTTP handler traverses AOI maps.
- [ ] Record the next optimization recommendation in
  `docs/benchmarks/aoi-enter-scan-observability.md`.

Do not start a new AOI algorithm change in this plan. The next optimization
must be chosen after comparing continuous benchmark skip rate with online
skip-rate data.
