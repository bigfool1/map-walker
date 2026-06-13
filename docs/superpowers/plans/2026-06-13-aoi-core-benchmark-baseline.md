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

- [x] Deliver reusable scenario definitions, player placement, active-player
  selection, and movement schedules.

**Task boundary:**

- Add a benchmark-only workload package independent from timing, profiling,
  subprocesses, and report output.
- Define scales of 100,000, 500,000, and 1,000,000 logical online Observers.
- Define 10,000 and 50,000 mover variants where applicable.
- Define sparse, normal, and 1%-hotspot distributions.
- Generate deterministic string IDs and Shanghai-local grid positions with
  fixed-seed perturbation.
- Use an explicit local `rand.Rand` created from the scenario seed; do not use
  package-global randomness.
- Sort map-derived values before they affect placement, mover selection, or
  movement generation.
- Generate one deterministic seeded-shuffle Build Order per scenario and reuse
  it for every Scenario Repeat.
- Generate Movement Patterns with exact 80% Cell-local and 20% Cell-crossing
  proportions.
- Treat entered and left Visibility Relationships as measured Visibility
  Churn, not as a third movement class.
- Generate direct AOI Core destinations and real World input/direction
  schedules.
- Generate one shared 120-AOI-Update trajectory constrained by real World
  movement speed.
- Store the trajectory as a Compact Movement Schedule with one mover-index set,
  compact per-update instructions, and reusable expansion buffers rather than
  retaining one object per move.
- Keep all generation outside measured execution.

**Behavioral goals:**

- The same commit, Go version, dependencies, configuration, and seed produce
  equivalent logical workloads.
- Cross-Go-version byte identity is not a requirement.
- Build uses the same distributed-arrival Build Order across repeats rather
  than coordinate order.
- Core and World-plus-AOI execute the same geometric AOI Update trajectory.
- Compact storage does not change mover counts, Movement Patterns, positions,
  or Visibility Churn.
- Sparse scenarios target an Initial Density of about 10 visible neighbors.
- Normal scenarios target an Initial Density of about 50 visible neighbors.
- Exactly 1% of players enter deterministic hotspot regions when scale permits.
- Hotspot players target an Initial Density of about 200-500 visible neighbors
  without creating one fully connected world.
- Record Steady-state Density after warm-up without requiring it to remain in
  the Initial Density target range.
- Every logical player remains both an Entity and an Observer.
- No external map or population dataset is required.

**Affected modules:**

- Create `internal/benchmark/aoiworkload/`
- Use public types from `internal/game/`

**Verification:**

- Test repeatability for IDs, positions, movers, trajectories, and World input
  schedules.
- Test workload generation does not depend on package-global randomness or map
  iteration order.
- Test the Build Order is a deterministic permutation of every player and is
  identical across Scenario Repeats.
- Test different seeds change perturbations without changing scenario
  constraints.
- Test Movement Patterns contain the exact 80% / 20% proportions.
- Test the exact proportions across the complete trajectory rather than within
  every individual AOI Update.
- Test Compact Movement Schedule expansion is deterministic and reaches the
  expected position at every AOI Update boundary.
- Test expansion reuses buffers and occurs outside measured execution.
- Test Visibility Churn is reported per mover with mean, P50, P95, and maximum
  entered-plus-left relationship counts.
- Test Initial Density ranges on deterministic samples.
- Test Steady-state Density is recorded after warm-up.
- Test the exact hotspot count.
- Test generated World schedules cause real movement through
  `ApplyInput` and `Step`.
- Run `go test ./internal/benchmark/aoiworkload`.

---

### Task 2: Define Results, Metrics, And Environment Metadata

- [x] Deliver stable result models and cross-platform measurement primitives.

**Task boundary:**

- Define scenario identity, phase, status, timing distributions, AOI counters,
  throughput, heap, allocation, GC, RSS, and environment metadata.
- Classify metrics as Primary or Diagnostic in the shared result model.
- Support `success`, `timeout`, `memory_limit`, `oom`, `signal`,
  `runtime_error`, and `not_applicable` statuses.
- Implement median, P95, P99, and maximum calculations.
- Capture Go version, GOOS/GOARCH, CPU count, `GOMAXPROCS`, Go GC settings,
  dependency versions, commit SHA, dirty-worktree flag, arguments, seed, and
  timestamp.
- Use each environment's default `GOMAXPROCS`; do not add a CPU-count scaling
  matrix in this phase.
- Capture Go heap and GC metrics through runtime APIs.
- Use Natural GC Runs: do not invoke `runtime.GC`, disable GC, or override
  `GOGC` or `GOMEMLIMIT` for ordinary measurements.
- Capture peak RSS through platform-specific macOS and Linux implementations,
  normalized to bytes.
- Keep expensive relationship traversal and metadata inspection outside timed
  regions.

**Behavioral goals:**

- Result schemas are shared by Go benchmarks, runners, commands, JSON, CSV, and
  reports.
- Primary Metrics headline capacity curves; Diagnostic Metrics remain available
  for explanation without becoming acceptance thresholds.
- Metrics never underflow when calculating before/after deltas.
- Platform-specific missing metadata is explicit rather than fabricated.
- Results are labeled as a Serial Core Baseline and make no claim about
  multi-core World or AOI scaling.
- Dirty worktrees are visible in every result.
- Failure results preserve partial metrics and the last known phase.
- Result metadata records effective `GOGC` and `GOMEMLIMIT`.
- Profile executions that require an explicit GC are marked and excluded from
  ordinary performance statistics.

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
- Test ordinary runners do not request an explicit GC or mutate GC settings.
- Test environment metadata and dirty-worktree recording.
- Test effective `GOMAXPROCS` is recorded without being overridden.
- Test platform RSS collection returns bytes or an explicit unavailable state.
- Test JSON round trips for success and every failure status.
- Test metric classification survives JSON and CSV serialization.
- Run `go test ./internal/benchmark/aoirunner`.

---

### Task 3: Implement AOIIndex Core Build And Tick Runners

- [ ] Deliver pure AOIIndex Build and steady-state movement measurements.

**Task boundary:**

- Build from an empty AOIIndex by calling public `Insert` once per player.
- Measure Build independently from workload generation and post-run
  relationship counting.
- Insert players using the scenario's Build Order.
- Emit a lightweight timestamped progress event at each 10% Build completion
  checkpoint and record cumulative elapsed time.
- Let the matrix parent sample child RSS asynchronously and associate each
  checkpoint with the nearest sample.
- Build the complete index before steady-state timing.
- Run the first 20 shared AOI Updates as warm-up and the remaining 100 as
  measured Ticks.
- Move 10,000 or 50,000 players per Tick through public `Move`.
- Record duration samples, moves/second, AOI counters, relationship totals,
  memory, GC, RSS, workload heap, and failure phase.
- Add only read-only production observation APIs if required; do not add batch
  insertion or change AOI behavior.

**Behavioral goals:**

- Build results report insertion cost separately from steady-state movement.
- Build results expose growth at 10% completion intervals without adding a
  second coordinate-order matrix.
- Build timing never performs a synchronous RSS query inside the Insert loop;
  checkpoint RSS is explicitly labeled as the nearest sampled value.
- Core timing excludes generation, Build, warm-up, profile setup, and expensive
  post-run traversal.
- Core timing excludes per-update Compact Movement Schedule expansion.
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
- Test Build follows the seeded shuffle and records all 10% checkpoints.
- Test checkpoint progress events contain timestamps and cumulative elapsed
  time without invoking synchronous RSS collection.
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
- Execute 40 warm-up and 200 measured 50ms simulation Ticks.
- Every second Tick consume moved IDs, read authoritative positions, and call
  AOIIndex `Move`.
- Record separate simulation, AOI preparation, and combined Tick
  distributions.
- Expand each Compact Movement Schedule instruction into reusable input and
  target buffers before its timed Tick.
- Record setup time for World population and AOI Build separately.
- Do not assemble replication changes, encode JSON, create Clients, or call
  Hub.

**Behavioral goals:**

- World positions are never directly mutated by benchmark code.
- Compact Movement Schedule expansion is excluded from simulation and AOI
  timing.
- The scenario represents 10 seconds of measured 20 Hz authoritative
  simulation and 10 Hz AOI preparation after a 2-second warm-up.
- The resulting 120 AOI Updates follow the same geometric trajectory as the
  Core runner.
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
- Test the first 40 simulation and 20 AOI Updates are excluded as warm-up.
- Test Core and World-plus-AOI positions agree at each AOI Update boundary.
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
- Profile output records whether collection itself required an explicit GC.
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

- [ ] Deliver a parent command that runs representative or exhaustive frozen
  scenarios independently and preserves failures.

**Task boundary:**

- Add `cmd/aoi-bench-matrix`.
- Run the Baseline Matrix by default:
  - 100,000 Observers / 10,000 movers / normal.
  - 1,000,000 Observers / 10,000 movers / normal.
  - 1,000,000 Observers / 50,000 movers / sparse, normal, and hotspot.
- Include one Build scenario for each scale and density represented in the
  Baseline Matrix; each scenario still has three Scenario Repeats.
- Add `--full` to expand every frozen scale, mover, density, and mode
  combination as the Full Matrix.
- Start each scenario as a separate `aoi-bench` process.
- Run three process-isolated Scenario Repeats for every measured Build, Core,
  and World-plus-AOI scenario in both matrices.
- Apply a configurable per-process timeout with a 15-minute default.
- Monitor child RSS from the parent process and apply a Memory Guard defaulting
  to 75% of physical memory.
- Support `--max-rss` to override or disable the Memory Guard without setting a
  Go runtime memory limit.
- Execute scenarios in ascending Observer-count order.
- Continue after timeout, signal termination, malformed output, or runtime
  error.
- Aggregate success and failure records to JSON and CSV.
- Capture elapsed time, exit code, signal, last phase, and bounded stderr
  summary.

**Behavioral goals:**

- Heap and GC state never carry between matrix scenarios.
- Heap and GC state never carry between Scenario Repeats.
- One million-player failure does not erase smaller completed results.
- Every Scenario Repeat has one raw result record, and every requested scenario
  has one aggregate record across its repeats.
- Default invocation remains practical for routine local baselines; exhaustive
  execution requires explicit `--full`.
- Timeout maps to `timeout`; Memory Guard termination maps to `memory_limit`;
  externally identifiable OOM maps to `oom`; other signal termination maps to
  `signal`; non-zero normal exit maps to `runtime_error`.
- JSON and CSV describe the same scenario set and statuses.
- Results preserve within-repeat Tick distributions and aggregate repeat-level
  median and peak-RSS variation as min, median, and max.
- Profile runs are explicitly marked and excluded from ordinary Scenario
  Repeats.
- The matrix can resume or rerun selected scenarios without changing workload
  identity.

**Affected modules:**

- Create `cmd/aoi-bench-matrix/main.go`
- Create `internal/benchmark/aoirunner/matrix.go`
- Create matrix and subprocess tests.
- Reuse result and workload models.

**Verification:**

- Test successful child aggregation.
- Test default Baseline Matrix membership and explicit Full Matrix expansion.
- Test the default timeout is 15 minutes and can be overridden.
- Test the Memory Guard defaults to 75% of physical memory and supports an
  explicit byte limit or disablement.
- Test scenarios are ordered by ascending Observer count.
- Test every measured scenario expands to three process-isolated repeats.
- Test repeat aggregation reports min, median, and max of per-repeat median
  duration and peak RSS.
- Test each failed repeat remains visible rather than being masked by successful
  repeats.
- Test timeout, memory-limit termination, identifiable OOM, other signal
  termination, non-zero exit, malformed JSON, and not-applicable scenarios.
- Test the matrix continues after every failure type.
- Test JSON and CSV contain matching identities and metrics.
- Run a small multi-process matrix end to end.
- Run `go test ./cmd/aoi-bench-matrix ./internal/benchmark/...`.

---

### Task 8: Produce And Document The Mac Logical Core Capacity Baseline

- [ ] Deliver reproducible baseline artifacts, profile summaries, and the
  engineering report.

**Task boundary:**

- Ensure all benchmark implementation changes are committed before producing
  public results.
- Run the Mac Baseline Matrix from a clean commit.
- Treat optional Mac `--full` runs as exploration, not as a completion
  requirement.
- Generate representative CPU and heap profiles for 100k/10k, 1m/10k, and
  1m/50k when those scenarios complete.
- Run profiles as separately marked executions excluded from ordinary repeat
  statistics.
- Commit aggregate JSON/CSV, reproduction commands, and text profile summaries.
- Do not commit raw `.pprof` files.
- Add a reserved section requiring the Full Matrix on the later 32 vCPU /
  64 GB Linux target.
- Update the handoff only after the baseline and project verification are
  complete.

**Behavioral goals:**

- The report distinguishes logical million Observers from real WebSocket
  connections.
- The report title and conclusions use Logical Core Capacity Baseline rather
  than online-player or production capacity.
- Failed, memory-limited, OOM, or signaled million scenarios remain visible.
- Build, Core Tick, and World-plus-AOI curves are reported separately.
- The report summary shows only Primary Metrics: Tick latency distributions,
  moves/second, peak RSS, heap, GC pause, and Visibility Churn.
- Diagnostic Metrics include candidate checks, distance checks, allocation
  detail, Build checkpoints, and other explanatory counters.
- Each report preserves within-repeat median/P95/P99/maximum and reports
  cross-repeat min/median/max for repeat medians and peak RSS.
- Time, relationship, heap, allocation, GC, and RSS findings are explained.
- Profile summaries identify evidence-backed optimization candidates without
  implementing them.
- Hardware, OS, Go version, commit SHA, runtime settings, seed, and commands
  make the experiment reproducible.
- Performance results are evidence, not CI thresholds or production claims.
- End-to-end capacity remains deferred until Hub, encoding, send queues, and
  real WebSocket connections are measured.
- Multi-core scaling remains deferred until Shards or parallel World/AOI
  execution exist.

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
