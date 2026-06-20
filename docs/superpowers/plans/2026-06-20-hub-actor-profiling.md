# Hub Actor Profiling Implementation Plan

> **For agentic workers:** Implement tasks in order. Track each task with its
> checkbox. This phase measures the current architecture only; do not optimize
> or split the actor while implementing this plan.

**Goal:** Build enough profiling and runtime metrics to determine whether
`Hub.Run()` is the next bottleneck as synthetic concurrency rises.

**Architecture:** Keep Hub as the single authoritative actor. Add low-overhead
observability around pprof, tick phases, actor handoff latency, replication
fanout, storage pressure, and runtime pressure. Use synthetic load plus pprof
to compare scaling across client counts and `GOMAXPROCS` values.

**Tech Stack:** Go 1.26, `net/http/pprof`, `runtime/metrics`, existing
synthetic clients, existing `HubSnapshot` and stats APIs, MySQL for load
matrix runs.

---

## Scope Guardrails

This plan does not change gameplay behavior, WebSocket protocol shape, AOI
semantics, persistence semantics, or the single-actor model. It only adds
measurement surfaces and a repeatable load-test procedure.

Do not optimize `Hub.Run()`, `AOIIndex`, replication encoding, WebSocket send
paths, storage writes, or synthetic clients in this phase. Any bottleneck found
here should become a separate follow-up plan.

Task 2, Task 3, and Task 4 share the Hub actor hot path and must be completed
by one agent in sequence. Task 1, Task 5a storage introspection, and Task 6 can
be worked in parallel because their edit surfaces are independent.

---

### Task 1: Enable Explicit pprof Surface

- [ ] Expose Go pprof endpoints from the server process behind an explicit
  profiling flag.

**Task boundary:**

- Do not rely on `net/http/pprof` registering handlers on
  `http.DefaultServeMux`; `internal/server/server.go` uses its own
  `http.NewServeMux()`.
- Add a profiling flag in `cmd/map-walker/`, default off.
- When profiling is enabled, either:
  - mount pprof handlers directly in `Server.Routes()` using `pprof.Index`,
    `pprof.Profile`, `pprof.Heap`, `pprof.Cmdline`, `pprof.Symbol`,
    `pprof.Trace`, and `pprof.Handler("goroutine"|"block"|"mutex")`; or
  - start a loopback-only internal profiling server using
    `http.DefaultServeMux`.
- If `/debug/pprof/block` and `/debug/pprof/mutex` are exposed, enable sampling
  early in `main.go` with `runtime.SetBlockProfileRate(...)` and
  `runtime.SetMutexProfileFraction(...)`.
- Keep profile endpoints off during normal local development unless the flag is
  explicitly set.

**Behavioral goals:**

- A running local server can serve CPU, heap, goroutine, block, mutex, and trace
  profiles when profiling is enabled.
- pprof availability does not change normal gameplay or synthetic client
  behavior when profiling is disabled.

**Affected modules:**

- `cmd/map-walker/`
- `internal/server/`

**Verification:**

- Run the server without the profile flag and confirm `/debug/pprof/` is not
  exposed.
- Run the server with the profile flag and confirm these endpoints respond:
  - `/debug/pprof/`
  - `/debug/pprof/profile?seconds=5`
  - `/debug/pprof/heap`
  - `/debug/pprof/goroutine`
  - `/debug/pprof/block`
  - `/debug/pprof/mutex`
  - `/debug/pprof/trace?seconds=1`
- Capture one CPU profile under synthetic load and inspect it with
  `go tool pprof -top`.

---

### Task 2: Add Hub Tick Phase Metrics

- [ ] Measure actor-loop phase durations and expose them through the stats
  snapshot surface.

**Task boundary:**

- Add a fixed-bucket histogram helper for duration metrics.
- Use one-second reset windows aligned with the existing Hub stats interval.
- Use fixed buckets with sub-millisecond precision for short phases and enough
  range to represent at least 200 ms tick overruns.
- Histogram writes must be allocation-free in steady state.
- Histogram writes happen only on the Hub actor goroutine; do not add locks to
  the actor hot path.
- Extend `HubSnapshot` or add a `HubProfileSnapshot` reachable from
  `/api/stats/synthetic` or a new `/api/stats/profile` endpoint.
- Record count, p50, p95, p99, and max for:
  - simulation tick
  - broadcast tick
  - collectible replacement advancement
  - pickup handling
  - persistence submit
  - leaderboard request handling

**Behavioral goals:**

- The server can show whether 20 Hz simulation approaches or exceeds the 50 ms
  tick budget.
- The server can show whether 10 Hz broadcast approaches or exceeds the 100 ms
  tick budget.
- Metrics distinguish periodic tick work from request-driven actor work.
- Metrics are readable without attaching pprof.

**Affected modules:**

- `internal/realtime/`
- `internal/server/`

**Verification:**

- Add focused tests for fixed-bucket percentile aggregation.
- Run `go test ./internal/realtime ./internal/server`.
- Under synthetic load, confirm tick phase metrics update once per stats window
  and remain bounded in memory.

---

### Task 3: Split Broadcast Profiling

- [ ] Measure the expensive substeps inside `broadcastReplication`.

**Task boundary:**

- Use names that match the current broadcast path; do not describe the hot path
  as full visible snapshot construction.
- Record duration for:
  - `snapshotMoverVisibility`
  - `applyMovementAOIChanges`
  - `advanceCollectibleReplacements`
  - `recalcCollectibleVisibility`
  - per-recipient `ReplicationChanges` accumulation through the `byRecipient`
    map
  - `TryEncodeReplicationUpdate`
  - `client.Send`
- Record payload bytes, replication recipients, and visible entity counts per
  recipient where the current data is already available.
- Avoid adding per-client allocations just to measure counts.

**Behavioral goals:**

- A broadcast slowdown can be attributed to AOI movement, old-neighbor capture,
  collectible visibility, reverse fanout, per-recipient change accumulation,
  JSON encoding, or send pressure.
- Measurements remain diagnostics only; they do not change replication output.

**Affected modules:**

- `internal/realtime/`
- `internal/game/`

**Verification:**

- Run `go test ./internal/realtime ./internal/game`.
- Run `go test ./internal/realtime -run '^$' -bench BenchmarkReplication -benchmem`.
- Capture a micro-benchmark CPU profile from
  `internal/realtime/replication_benchmark_test.go` and compare its top
  functions with the broadcast subphase that reports the highest duration.
- Run a synthetic load scenario and confirm broadcast p95/p99 can be broken
  down by subphase.

---

### Task 4: Add Actor Handoff Latency Metrics

- [ ] Measure sender wait time for unbuffered Hub channels.

**Task boundary:**

- Rename this concept to actor handoff latency, not queue latency.
- Current Hub channels are unbuffered, so send-before timestamp to actor receive
  duration means sender block time.
- Do not add channel buffers in this phase.
- Attach send-before timestamps where practical for:
  - input
  - collect
  - register
  - unregister
  - disconnect user
  - appearance update
- Treat leaderboard handoff latency as optional because it is synchronous,
  request/reply, and expected to be low QPS.
- Add a drop counter for `SubmitCollect`, because it uses a non-blocking send
  with `default` and dropped collect attempts otherwise remain invisible.

**Behavioral goals:**

- Handoff latency rises when actor service capacity is lower than incoming
  event rate.
- Input handoff latency can be compared directly with tick overrun and pprof
  CPU data.
- Dropped collect attempts are visible in stats.

**Affected modules:**

- `internal/realtime/`
- `internal/server/`

**Verification:**

- Run `go test ./internal/realtime ./internal/server`.
- Under increasing synthetic load, confirm handoff latency metrics are visible
  for input and collect paths.
- Confirm idle or low-load handoff latency is near zero.
- Confirm a forced `SubmitCollect` drop increments the drop counter.

---

### Task 5a: Add Storage Introspection

- [ ] Add minimal storage pressure snapshots to persistence components.

**Task boundary:**

- Add introspection to `PersistenceWorker` for queue depth, recent write
  duration, write failure count, and retry count.
- Add introspection to `ScorePersister` for queue depth, recent write duration,
  write failure count, retry count, and sync flush duration.
- Keep these as lightweight in-memory counters.
- Do not add SQLite load-matrix parity work; MySQL is the target backend for
  load profiling.

**Behavioral goals:**

- Storage pressure can be ruled in or ruled out when Hub tick latency rises.
- Storage counters are readable without blocking Hub work.

**Affected modules:**

- `internal/storage/`
- `internal/realtime/`

**Verification:**

- Run `go test ./internal/storage ./internal/realtime`.
- Add tests that confirm queue depth and failure counters move when persistence
  submissions are accepted, retried, or flushed.

---

### Task 5b: Add Runtime And External Bottleneck Snapshot

- [ ] Summarize non-Hub pressure alongside Hub metrics.

**Task boundary:**

- Pull storage introspection into the Hub or stats snapshot surface.
- Record WebSocket send failures or full-send events.
- Record heap, GC pause summary, goroutine count, CPU class samples, and
  effective `GOMAXPROCS` using `runtime/metrics`.
- Do not use frequent `runtime.ReadMemStats` polling for this profile surface.

**Behavioral goals:**

- The profile can distinguish Hub CPU saturation from network write pressure,
  database pressure, GC pressure, and goroutine buildup.
- A bottleneck report can explain why the actor is or is not the next limit.

**Affected modules:**

- `internal/realtime/`
- `internal/server/`

**Verification:**

- Run `go test ./internal/realtime ./internal/server`.
- Run a synthetic load scenario and confirm external counters change when load
  increases.
- Confirm runtime counters are included in the same snapshot or report as Hub
  metrics.

---

### Task 6: Define Load-Test Matrix And Report Format

- [ ] Document and automate the profiling run procedure.

**Task boundary:**

- Use MySQL for load-matrix runs. SQLite remains for local development and unit
  tests, not profiling conclusions.
- Record backend metadata: MySQL version, DSN label without secrets,
  connection pool size, `max_connections`, and whether MySQL is colocated with
  the server process.
- Record hardware metadata: CPU model, core count, RAM, OS, Go version, commit,
  dirty-worktree state, and effective `GOMAXPROCS`.
- Record synthetic topology: in-process synthetic clients or separate process,
  plus whether synthetic load is colocated with the server.
- Define the full client-count matrix: 100, 300, 500, 1000, and 2000 synthetic
  clients.
- Define CPU parallelism steps: `GOMAXPROCS=1`, `2`, `4`, and `8`.
- Allow a quick first-pass diagonal matrix before the full matrix:
  - 100 clients with `GOMAXPROCS=1`
  - 500 clients with `GOMAXPROCS=4`
  - 2000 clients with `GOMAXPROCS=8`
- Run each step long enough to ignore ramp-up and capture a stable window.
- Capture one 30-second CPU profile per stable window.
- Capture heap and goroutine profiles at the highest stable client count.
- Ignore synthetic ramp-up when interpreting steady-state windows.
- Produce a markdown report under `docs/benchmarks/`.
- Add or confirm `.gitignore` excludes `docs/benchmarks/profiles/*.pprof`.

**Behavioral goals:**

- Results can be compared between commits.
- The quick diagonal matrix gives early signal without requiring the full
  5-by-4 matrix.
- The full 5-by-4 matrix remains available for final confirmation, with the
  expected cost called out as more than one hour of runtime on a local machine.
- Each row records client count, `GOMAXPROCS`, backend, hardware, synthetic
  topology, CPU profile top function, tick p95/p99, handoff p95/p99, payload
  bytes/sec, DB p95, GC p99, and observed bottleneck.

**Affected modules:**

- `docs/benchmarks/`
- `.gitignore`
- Optional helper under `docs/benchmarks/` or `cmd/`

**Verification:**

- Run at least the quick diagonal matrix locally.
- Confirm the generated report includes commands, environment metadata, backend
  metadata, synthetic topology, and bottleneck interpretation.
- Confirm large `.pprof` binaries are ignored by git.

---

## Observer Effect Verification

After Task 2 through Task 5 are complete, verify the instrumentation itself did
not materially distort the hot paths.

- [ ] Run `go test ./internal/realtime -run AOIScale`.
- [ ] Run `go test ./internal/realtime -run CollectibleScale`.
- [ ] Run `go test ./internal/realtime -run '^$' -bench BenchmarkReplication -benchmem`.
- [ ] Compare AOI candidate counts, distance-check counts, replication bytes,
  and benchmark `allocs/op` against the nearest checked-in baseline or the
  pre-instrumentation run from the same machine.

If the instrumentation changes key counts or materially increases allocation
rates, fix the measurement code before using the profiling output for
architecture decisions.

---

## Bottleneck Decision Rule

Treat the Hub actor as the confirmed next bottleneck only when all of these are
true in the same load window:

- Increasing `GOMAXPROCS` no longer materially improves throughput or latency.
- One CPU core is near saturation while total CPU still has headroom.
- CPU profile samples are dominated by `Hub.Run()` and synchronous callees such
  as `World.Step`, AOI updates, replication construction, JSON encoding, or
  other in-actor work.
- Simulation or broadcast p95/p99 approaches or exceeds its tick budget.
- Actor handoff latency rises with client count.
- WebSocket send pressure, database writes, GC, and goroutine buildup are not
  already the dominant bottleneck.

If any condition is false, the report should name the observed bottleneck
instead of attributing the limit to the single actor.

---

## Documentation Update

When this plan is completed, update `docs/map-walker-handoff.md` with only the
current phase details: profile flag, pprof endpoints, new stats fields, load
matrix command shape, and known limits. Do not accumulate historical phase
details there.
