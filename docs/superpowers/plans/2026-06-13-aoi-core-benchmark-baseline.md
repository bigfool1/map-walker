# AOI Core Benchmark Baseline Implementation Plan

> **For agentic workers:** Implement tasks in order. Use the approved design as
> the source of truth and track each task with its checkbox.

**Goal:** Build a deterministic, reproducible baseline for current AOIIndex and
World-plus-AOI performance without optimizing production algorithms.

**Architecture:** A frozen workload package generates logical online-player
scenarios. A runner package measures AOI Core and World-plus-AOI paths.
Single-scenario and matrix commands isolate large experiments in child
processes and produce structured results and a checked-in Mac report.

**Tech Stack:** Go 1.26, `testing.B`, Go runtime metrics, `runtime/pprof`,
JSON/CSV artifacts, macOS and Linux process metrics.

**Required context:** Read `AGENTS.md`, `docs/map-walker-handoff.md`, and
`docs/superpowers/specs/2026-06-13-aoi-core-benchmark-baseline-design.md`
before implementation. The approved design is authoritative when this plan is
ambiguous.

---

## Scope Guardrails

This plan measures the existing AOIIndex and World behavior. It does not
optimize AOI, change IDs or adjacency representation, add Entity/Observer
separation, enter Hub or WebSocket paths, implement Shards, or add gameplay.

Million-player scenarios are explicit benchmark commands, not part of normal
`go test ./...`. Public baseline artifacts must be generated from a clean,
recorded commit. Existing unrelated worktree changes must not be reverted or
silently included.

---

### Task 1: Freeze Deterministic AOI Workloads

- [ ] Deliver reusable scenario definitions, player placement, active-player
  selection, and movement schedules.

**Task boundary:**

- Add a benchmark-only workload package independent from timing, profiling,
  subprocesses, and report output.
- Define scales of 100,000, 500,000, and 1,000,000 logical online Observers.
- Define 10,000 and 50,000 mover variants where applicable.
- Define sparse, normal, and 1%-hotspot distributions.
- Generate deterministic string IDs and Shanghai-local grid positions with
  fixed-seed perturbation.
- Generate movement classes with exact 80% Cell-local, 15% Cell-crossing, and
  5% AOI-threshold-crossing proportions.
- Generate direct AOI Core destinations and real World input/direction
  schedules.
- Keep all generation outside measured execution.

**Behavioral goals:**

- The same configuration and seed produce byte-for-byte equivalent logical
  workloads.
- Sparse scenarios target about 10 visible neighbors.
- Normal scenarios target about 50 visible neighbors.
- Exactly 1% of players enter deterministic hotspot regions when scale permits.
- Hotspot players target about 200-500 visible neighbors without creating one
  fully connected world.
- Every logical player remains both an Entity and an Observer.
- No external map or population dataset is required.

**Affected modules:**

- Create `internal/benchmark/aoiworkload/`
- Use public types from `internal/game/`

**Verification:**

- Test repeatability for IDs, positions, movers, trajectories, and World input
  schedules.
- Test different seeds change perturbations without changing scenario
  constraints.
- Test density ranges on deterministic samples.
- Test exact hotspot and 80/15/5 movement-class counts.
- Test generated World schedules cause real movement through
  `ApplyInput` and `Step`.
- Run `go test ./internal/benchmark/aoiworkload`.

---

### Task 2: Define Results, Metrics, And Environment Metadata

- [ ] Deliver stable result models and cross-platform measurement primitives.

**Task boundary:**

- Define scenario identity, phase, status, timing distributions, AOI counters,
  throughput, heap, allocation, GC, RSS, and environment metadata.
- Support `success`, `timeout`, `oom_or_signal`, `runtime_error`, and
  `not_applicable` statuses.
- Implement median, P95, P99, and maximum calculations.
- Capture Go version, GOOS/GOARCH, CPU count, `GOMAXPROCS`, Go GC settings,
  commit SHA, dirty-worktree flag, arguments, seed, and timestamp.
- Capture Go heap and GC metrics through runtime APIs.
- Capture peak RSS through platform-specific macOS and Linux implementations,
  normalized to bytes.
- Keep expensive relationship traversal and metadata inspection outside timed
  regions.

**Behavioral goals:**

- Result schemas are shared by Go benchmarks, runners, commands, JSON, CSV, and
  reports.
- Metrics never underflow when calculating before/after deltas.
- Platform-specific missing metadata is explicit rather than fabricated.
- Dirty worktrees are visible in every result.
- Failure results preserve partial metrics and the last known phase.

**Affected modules:**

- Create `internal/benchmark/aoirunner/result.go`
- Create `internal/benchmark/aoirunner/metrics.go`
- Create platform-specific RSS files under
  `internal/benchmark/aoirunner/`
- Create corresponding tests in `internal/benchmark/aoirunner/`

**Verification:**

- Test percentile values with fixed duration samples.
- Test byte and duration unit normalization.
- Test heap, allocation, and GC delta calculations.
- Test environment metadata and dirty-worktree recording.
- Test platform RSS collection returns bytes or an explicit unavailable state.
- Test JSON round trips for success and every failure status.
- Run `go test ./internal/benchmark/aoirunner`.

---

### Task 3: Implement AOIIndex Core Build And Tick Runners

- [ ] Deliver pure AOIIndex Build and steady-state movement measurements.

**Task boundary:**

- Build from an empty AOIIndex by calling public `Insert` once per player.
- Measure Build independently from workload generation and post-run
  relationship counting.
- Build the complete index before steady-state timing.
- Run 20 warm-up Ticks and 100 measured Ticks.
- Move 10,000 or 50,000 players per Tick through public `Move`.
- Record duration samples, moves/second, AOI counters, relationship totals,
  memory, GC, RSS, and failure phase.
- Add only read-only production observation APIs if required; do not add batch
  insertion or change AOI behavior.

**Behavioral goals:**

- Build results report insertion cost separately from steady-state movement.
- Core timing excludes generation, Build, warm-up, profile setup, and expensive
  post-run traversal.
- The runner uses the current string-ID and symmetric adjacency implementation.
- Million-player timeout or memory failure remains a valid structured result.
- Repeated small scenarios produce stable relationship and AOI counter totals.

**Affected modules:**

- Create `internal/benchmark/aoirunner/core.go`
- Create `internal/benchmark/aoirunner/core_test.go`
- Modify `internal/game/aoi.go` only for strictly read-only observation required
  by the report.
- Extend `internal/game/aoi_test.go` if a read-only observation API is added.

**Verification:**

- Test small known Build relationship totals and AOI counters.
- Test warm-up and generation are excluded from measured Tick samples.
- Test exactly 100 Core Tick samples are reported.
- Test throughput and before/after relationship totals.
- Test the runner uses `Insert` and `Move` rather than direct index mutation.
- Run `go test ./internal/benchmark/aoirunner ./internal/game`.

---

### Task 4: Implement World Plus AOI Simulation Runner

- [ ] Deliver authoritative movement and AOI preparation measurements using the
  real World path.

**Task boundary:**

- Add every scenario player through public World registration APIs.
- Insert the same players into AOIIndex through public `Insert`.
- Drive active players through real `World.ApplyInput`.
- Execute 200 measured 50ms simulation Ticks.
- Every second Tick consume moved IDs, read authoritative positions, and call
  AOIIndex `Move`.
- Record separate simulation, AOI preparation, and combined Tick
  distributions.
- Record setup time for World population and AOI Build separately.
- Do not assemble replication changes, encode JSON, create Clients, or call
  Hub.

**Behavioral goals:**

- World positions are never directly mutated by benchmark code.
- The scenario represents 10 seconds of 20 Hz authoritative simulation and
  10 Hz AOI preparation.
- Reports identify simulation Ticks over 50ms and AOI preparation over 100ms.
- Reports calculate measured remaining budget before unmeasured Hub work.
- The report does not present remaining budget as production capacity.
- Core and World-plus-AOI results remain directly distinguishable.

**Affected modules:**

- Create `internal/benchmark/aoirunner/world.go`
- Create `internal/benchmark/aoirunner/world_test.go`
- Use `internal/game/world.go` and `internal/game/aoi.go` through public APIs.

**Verification:**

- Test real `ApplyInput`, `Step`, moved-ID consumption, position lookup, and
  AOI updates in a small scenario.
- Test exactly 200 simulation samples and 100 AOI preparation samples.
- Test separate simulation, AOI, and combined percentiles.
- Test budget-exceeded counts and remaining-budget calculations.
- Test no Hub, Client, encoding, or direct World position mutation is used.
- Run `go test ./internal/benchmark/aoirunner ./internal/game`.

---

### Task 5: Add Standard Go Benchmarks And Profile Hooks

- [ ] Deliver `testing.B` entry points for repeatable local comparison and
  profile generation.

**Task boundary:**

- Add Go benchmarks for AOI Build, AOI steady-state Move, and World plus AOI.
- Reuse frozen workload definitions rather than defining separate benchmark
  scenarios.
- Support `-benchmem`, CPU profiles, and heap profiles through standard Go
  tooling.
- Provide practical smaller defaults for fast local profiling while keeping
  representative named scenarios available.
- Do not run the full matrix as part of ordinary benchmark invocation.

**Behavioral goals:**

- Later optimization work can compare the same named workload with `benchstat`.
- Benchmark setup and generation are excluded from the measured operation when
  measuring steady-state movement.
- Profile output reflects the targeted benchmark layer.
- Existing production AOI behavior is not modified for benchmark convenience.

**Affected modules:**

- Create benchmark `_test.go` files under
  `internal/benchmark/aoirunner/`
- Reuse `internal/benchmark/aoiworkload/`

**Verification:**

- Run selected benchmarks with `-benchmem`.
- Generate a small CPU profile and confirm `go tool pprof` can read it.
- Generate a small heap profile and confirm `go tool pprof` can read it.
- Confirm named benchmarks reuse the frozen scenario definitions.
- Run `go test ./internal/benchmark/aoirunner`.

---

### Task 6: Add The Single-Scenario Benchmark Command

- [ ] Deliver a process-isolated CLI that runs one Build, Core Tick, or
  World-plus-AOI scenario.

**Task boundary:**

- Add `cmd/aoi-bench`.
- Accept one explicit mode, scale, mover count, density, seed, and output
  configuration.
- Emit exactly one machine-readable JSON result to stdout.
- Send progress and diagnostics to stderr.
- Optionally create CPU and heap profile files.
- Track the last phase so parent processes can preserve partial failure
  context.
- Reject invalid or inapplicable configurations clearly.

**Behavioral goals:**

- One process owns one scenario and exits after that scenario.
- Successful stdout remains valid JSON without diagnostic contamination.
- Runtime errors exit non-zero.
- Inapplicable scenarios produce a structured `not_applicable` result.
- Profile generation occurs only when explicitly requested.
- Running against a dirty worktree is allowed for exploration but visibly
  recorded.

**Affected modules:**

- Create `cmd/aoi-bench/main.go`
- Create command-level parsing and output tests where practical.
- Use `internal/benchmark/aoiworkload/`
- Use `internal/benchmark/aoirunner/`

**Verification:**

- Run small Build, Core Tick, and World-plus-AOI scenarios.
- Parse stdout as one JSON result.
- Verify diagnostics stay on stderr.
- Verify invalid arguments, runtime failure, and not-applicable behavior.
- Verify optional CPU and heap files are created only when requested.
- Run `go test ./cmd/aoi-bench ./internal/benchmark/...`.

---

### Task 7: Add Matrix Process Isolation And JSON/CSV Aggregation

- [ ] Deliver a parent command that runs every frozen scenario independently
  and preserves failures.

**Task boundary:**

- Add `cmd/aoi-bench-matrix`.
- Expand Build, Core Tick, and World-plus-AOI matrices from frozen scenario
  definitions.
- Start each scenario as a separate `aoi-bench` process.
- Repeat Build scenarios three times.
- Apply a configurable per-process timeout.
- Continue after timeout, signal termination, malformed output, or runtime
  error.
- Aggregate success and failure records to JSON and CSV.
- Capture elapsed time, exit code, signal, last phase, and bounded stderr
  summary.

**Behavioral goals:**

- Heap and GC state never carry between matrix scenarios.
- One million-player failure does not erase smaller completed results.
- Every requested scenario has exactly one aggregate record per run.
- Timeout maps to `timeout`; signal termination maps to `oom_or_signal`;
  non-zero normal exit maps to `runtime_error`.
- JSON and CSV describe the same scenario set and statuses.
- The matrix can resume or rerun selected scenarios without changing workload
  identity.

**Affected modules:**

- Create `cmd/aoi-bench-matrix/main.go`
- Create `internal/benchmark/aoirunner/matrix.go`
- Create matrix and subprocess tests.
- Reuse result and workload models.

**Verification:**

- Test successful child aggregation.
- Test timeout, non-zero exit, signal termination, malformed JSON, and
  not-applicable scenarios.
- Test the matrix continues after every failure type.
- Test JSON and CSV contain matching identities and metrics.
- Run a small multi-process matrix end to end.
- Run `go test ./cmd/aoi-bench-matrix ./internal/benchmark/...`.

---

### Task 8: Produce And Document The Mac Baseline

- [ ] Deliver reproducible baseline artifacts, profile summaries, and the
  engineering report.

**Task boundary:**

- Ensure all benchmark implementation changes are committed before producing
  public results.
- Run the complete Mac matrix from a clean commit.
- Record 100,000, 500,000, and 1,000,000 Observer scenarios with 10,000 and
  50,000 movers where applicable.
- Include sparse, normal, and hotspot density scenarios.
- Generate representative CPU and heap profiles for 100k/10k, 1m/10k, and
  1m/50k when those scenarios complete.
- Commit aggregate JSON/CSV, reproduction commands, and text profile summaries.
- Do not commit raw `.pprof` files.
- Add a reserved section for later 32 vCPU / 64 GB Linux results.
- Update the handoff only after the baseline and project verification are
  complete.

**Behavioral goals:**

- The report distinguishes logical million Observers from real WebSocket
  connections.
- Failed or OOM million scenarios remain visible.
- Build, Core Tick, and World-plus-AOI curves are reported separately.
- Time, relationship, heap, allocation, GC, and RSS findings are explained.
- Profile summaries identify evidence-backed optimization candidates without
  implementing them.
- Hardware, OS, Go version, commit SHA, runtime settings, seed, and commands
  make the experiment reproducible.
- Performance results are evidence, not CI thresholds or production claims.

**Affected modules:**

- Create `docs/benchmarks/aoi-core-baseline.md`
- Create a benchmark result directory under `docs/benchmarks/`
- Add raw `.pprof` patterns to `.gitignore` if not already ignored.
- Update `docs/map-walker-handoff.md` after completion.

**Verification:**

- Confirm the recorded baseline commit is clean and matches report metadata.
- Validate aggregate JSON and CSV parse successfully.
- Confirm every matrix scenario has a success, failure, or not-applicable
  record.
- Confirm profile summaries match the representative commands.
- Re-run selected small scenarios from the documented commands.
- Run `go test ./...`.
- Run `go vet ./...`.

---

## Final Acceptance

- [ ] Review implementation against every acceptance criterion in
  `docs/superpowers/specs/2026-06-13-aoi-core-benchmark-baseline-design.md`.
- [ ] Confirm production AOI and World algorithms were not optimized or
  behaviorally changed.
- [ ] Confirm AOI Core and World-plus-AOI are measured independently.
- [ ] Confirm million-player experiments remain outside normal tests.
- [ ] Confirm public Mac artifacts come from a clean recorded commit.
- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...`.

