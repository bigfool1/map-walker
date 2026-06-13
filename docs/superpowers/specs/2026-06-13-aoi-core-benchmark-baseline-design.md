# AOI Core Benchmark Baseline Design

Date: 2026-06-13

## Goal

Build a reproducible Logical Core Capacity Baseline for the current AOI and
authoritative movement implementation before making performance optimizations.

The target game model is one million genuinely online players. In the logical
workload, every player is both:

- An Entity that exists in the spatial world and can be observed.
- An Observer that owns a visible-neighbor set.

The baseline does not claim that one process or one machine can serve one
million real connections. It measures the current single-process core so later
work can determine a realistic per-Shard capacity and derive how many Shards
would be required for one million online players.

## Phase Order

The broader project sequence is:

1. AOI Core benchmark baseline and profiling.
2. Profile-driven AOI optimization in a separate design.
3. A small server-authoritative gameplay vertical slice.
4. Hub, encoding, send-queue, and localhost WebSocket load testing.
5. Single-process multi-Shard design if measurements justify it.

This specification covers only step 1.

## Scope

This phase includes:

- A frozen deterministic workload model.
- AOIIndex Core Build and steady-state movement baselines.
- World plus AOI simulation baselines.
- 100,000, 500,000, and 1,000,000 Observer scenarios.
- 10,000 and 50,000 moving-player scenarios when the scale permits them.
- Sparse, normal, and hotspot density models.
- Standard Go benchmarks for local comparison and profiling.
- Standalone single-scenario and matrix runners.
- Independent child processes for every matrix scenario.
- Structured JSON and CSV results.
- CPU, heap, allocation, GC, and peak RSS measurements.
- A checked-in Mac baseline report.
- A report section for later 32 vCPU / 64 GB Linux results.
- Structured failure results for timeout, memory limit, OOM, signal, and
  runtime errors.

This phase excludes:

- Changes intended to optimize AOIIndex or World.
- Integer entity IDs, compact adjacency sets, pooling, or bulk index building.
- Entity/Observer separation.
- Hub actor processing.
- JSON or binary protocol encoding.
- Client send queues.
- Real or synthetic WebSocket connections.
- Gateway, Shard, cross-Shard migration, or Ghost entities.
- Gameplay, NPCs, resources, combat, or interaction rules.
- CI performance thresholds.
- A claim of production capacity.
- A `GOMAXPROCS` or CPU-count scaling study.

## Baseline Rule

The current implementation is the baseline under test.

Benchmark construction must use the same public behavior as production:

- AOI Build calls `AOIIndex.Insert` once for every player.
- AOI movement calls `AOIIndex.Move`.
- World movement calls `World.ApplyInput` and `World.Step`.
- World-to-AOI updates consume moved player IDs and current authoritative
  positions.

The benchmark must not add a batch insertion shortcut, directly manufacture
Cell maps, directly manufacture adjacency sets, or bypass World movement.

If observation requires a missing API, this phase may add a read-only metric or
inspection method that does not change algorithm behavior. Such observation
must run outside timed regions when it would materially affect the result.

A 1,000,000-player scenario that times out, runs out of memory, or is killed is
a valid baseline result. It is recorded rather than repaired in this phase.

## Benchmark Layers

### Layer 1: AOIIndex Core

This layer measures spatial indexing and visibility maintenance without World,
Hub, encoding, or networking.

Build path:

```text
generated player positions
  -> AOIIndex.Insert for every player
  -> Build metrics
```

Steady-state Tick path:

```text
Compact Movement Schedule expansion
  -> AOIIndex.Move for each moving player
  -> AOI relation and timing metrics
```

This layer isolates:

- Cell insertion and movement.
- Nine-Cell candidate collection.
- Exact distance checks.
- Symmetric adjacency storage.
- Hysteresis relation changes.
- String ID, map, slice, sorting, allocation, and GC costs.

### Layer 2: World Plus AOI Simulation

This layer measures authoritative movement and AOI together, still without Hub
or networking.

Setup:

- Add every player to `game.World`.
- Insert every player into `AOIIndex`.
- Give the configured active players real directional input through
  `World.ApplyInput`.

Measured path:

```text
20 Hz:
  World.Step(50ms)

every second simulation Tick:
  World.TakeMovedPlayerIDs
  World.PlayerPositions
  AOIIndex.Move
```

This layer measures:

- World player-state memory.
- Input state and authoritative movement.
- Moved-player tracking.
- 20 Hz simulation Tick cost.
- 10 Hz AOI preparation cost.
- Combined Tick cost before Hub replication work.

It does not assemble per-client replication changes or encode messages.

## Frozen Workload Model

### Scales

Observer counts:

- 100,000
- 500,000
- 1,000,000

Every Observer is also an Entity and every Entity is also an Observer.

Moving-player counts:

- 10,000
- 50,000

A scenario whose moving-player count exceeds its Observer count is skipped and
recorded as not applicable.

### Player IDs

The baseline uses the current string-ID model. IDs are deterministic and unique
for the full scale. The benchmark does not replace them with integer IDs.

### Distribution Generation

Player positions use a regular local-meter grid with a small deterministic
perturbation.

- Grid spacing controls the target Initial Density.
- A fixed random seed controls perturbation and movement selection.
- Randomness comes from a scenario-local `rand.Rand`; package-global randomness
  is not used.
- Values originating from maps are sorted before they influence generation.
- Each scenario generates one deterministic seeded-shuffle Build Order reused
  by all Scenario Repeats.
- Generation completes before timed work.
- The generator validates the achieved Initial Density on deterministic samples
  and records the observed relationship counts.

The benchmark uses Shanghai-local coordinates through the existing
`game.AOIConfig`. No map provider or geographic dataset is loaded.

### Density Scenarios

#### Sparse

Target an Initial Density of approximately 10 visible neighbors per player
immediately after Build.

This scenario shows index and player-state overhead when adjacency is small.

#### Normal

Target an Initial Density of approximately 50 visible neighbors per player
immediately after Build.

This is the primary one-million-Observer logical core workload.

#### Hotspot

- 99% of players use the normal distribution.
- 1% occupy deterministic high-density regions.
- Players in the hotspot target an Initial Density of approximately 200-500
  visible neighbors.
- The hotspot must not collapse all players into one fully connected region.

This scenario measures uneven urban density and adjacency-tail costs.

Initial Density is measured immediately after Build and reflects relationships
established within the enter radius. After warm-up, the benchmark records
Steady-state Density separately so retained relationships within the AOI
hysteresis band remain visible in the results. Steady-state Density is not
required to remain inside the Initial Density target range.

### Movement Patterns

Moving-player trajectories are generated before timed execution:

- 80% perform small movement that remains within the current Cell.
- 20% cross a Cell boundary.

The selection and trajectory schedule use an explicit local random source with
a fixed seed. Each scenario produces one 120-AOI-Update trajectory constrained
by the real World's movement speed.
Random-number generation, position planning, and trajectory classification are
excluded from timed sections.

The trajectory is retained as a Compact Movement Schedule:

- Player IDs are stored once and movers are referenced by index.
- Each AOI Update stores compact deterministic movement instructions.
- Instructions expand into reusable target-position and World-input buffers
  immediately before the timed work for that update.
- Expanded per-move objects are not retained across the complete run.
- Expansion is excluded from Core, simulation, and AOI timing.

Movement Pattern describes only the trajectory's geometric relationship to
Cell boundaries. Entered and left Visibility Relationships are measured
separately as Visibility Churn. Each scenario reports entered-plus-left
relationship counts per mover as mean, P50, P95, and maximum values.

For AOIIndex Core, the generated destinations are passed directly to
`AOIIndex.Move`.

For World plus AOI, initial placement and direction schedules are arranged so
real `ApplyInput` and `Step` movement reach the same positions at AOI Update
boundaries. World positions are never directly mutated by the benchmark.

The exact 80% / 20% Movement Pattern mix applies across the complete shared
trajectory, not independently within each AOI Update.

## Build Measurement

Build is measured separately from steady-state movement because Shard startup
or recovery has different requirements from normal Tick processing.

For each scale and density:

- Start from an empty AOIIndex.
- Insert players through `AOIIndex.Insert` using the scenario's Build Order.
- Run the Build scenario in an independent process.
- Run three process-isolated Scenario Repeats using the same Build Order.

Record:

- Wall-clock Build duration.
- Inserts per second.
- Total visible relationship pairs after Build.
- AOI candidate checks.
- Exact distance checks.
- Go heap metrics.
- Allocation totals.
- GC count and pause data.
- Peak process RSS.
- Cumulative elapsed time at each 10% Build completion checkpoint.
- The nearest asynchronously sampled RSS value for each checkpoint.
- Success or structured failure.

Relationship counting may occur after the timed Build region if obtaining the
count requires a full traversal.

Build Order models distributed arrival rather than coordinate-ordered bulk
loading. Coordinate-order comparison is deferred until a future Build
optimization study.

The child emits only lightweight timestamped progress events inside the Build
loop. The matrix parent samples child RSS independently and associates each
checkpoint with the nearest sample. Checkpoint RSS is labeled as sampled rather
than exact, and no synchronous RSS query runs inside the timed Insert loop.
Final heap and GC metrics are collected after Build.

World plus AOI setup is also measured and reported separately:

- Time to add all World players.
- Time to build AOI membership and relationships.
- World and AOI memory after setup.

## AOIIndex Steady-State Measurement

Each Core Tick scenario:

1. Builds the complete AOIIndex before timing.
2. Executes the first 20 shared AOI Updates as warm-up.
3. Executes the remaining 100 shared AOI Updates as measured Ticks.
4. Applies 10,000 or 50,000 expanded moves per Tick.

Report:

- Median Tick duration.
- P95 Tick duration.
- P99 Tick duration.
- Maximum Tick duration.
- Moves per second.
- Candidate checks per Tick.
- Exact distance checks per Tick.
- Relationships entered and left per Tick.
- Relationship count before and after the run.
- Heap and allocation deltas.
- GC count, total pause, longest observed pause, and pause contribution.
- Peak RSS.

The baseline does not require the Tick to complete within 100ms. The measured
curve is used to determine capacity.

## World Plus AOI Measurement

Each World plus AOI scenario executes 40 warm-up simulation Ticks followed by
200 measured simulation Ticks, representing 2 seconds of warm-up and 10 seconds
of measured game time:

- Every Tick executes `World.Step(50ms)`.
- Every second Tick consumes moved player IDs and updates AOIIndex.
- Active-player direction changes use the Compact Movement Schedule and real
  `World.ApplyInput`.
- The 120 resulting AOI Updates follow the same geometric trajectory as the
  Core runner.

Report separate duration distributions for:

- 20 Hz World simulation Ticks.
- 10 Hz AOI update work.
- Combined simulation-plus-AOI Ticks.

For each distribution report:

- Median.
- P95.
- P99.
- Maximum.

Also report:

- Moved players per Tick.
- Candidate and distance checks.
- Relationship changes.
- Heap, allocation, GC, and peak RSS metrics.
- Whether simulation exceeded its 50ms budget.
- Whether AOI preparation exceeded its 100ms budget.
- Remaining measured budget before Hub replication work.

The report must not interpret remaining time as a production guarantee because
Hub, encoding, send queues, sockets, and operating-system networking are absent.

## Runner Architecture

### Workload Package

`internal/benchmark/aoiworkload` owns:

- Frozen scenario definitions.
- Deterministic IDs.
- Grid and perturbation generation.
- Density validation.
- Active-player selection.
- Precomputed Core trajectories.
- Precomputed World input schedules.

It contains no timing, profiling, subprocess, or report-writing logic.

### Runner Package

`internal/benchmark/aoirunner` owns:

- AOI Core Build execution.
- AOI Core steady-state execution.
- World plus AOI execution.
- Duration samples and percentile calculation.
- AOI metric collection.
- Heap, allocation, GC, and RSS collection.
- Structured result models.

It does not choose matrix scenarios or start child processes.

### Single-Scenario Command

`cmd/aoi-bench`:

- Runs exactly one requested scenario in one process.
- Supports Build, Core Tick, and World plus AOI modes.
- Emits one structured JSON result.
- Writes diagnostics to stderr.
- Optionally creates CPU and heap profiles.
- Exits non-zero for runtime errors.

### Matrix Command

`cmd/aoi-bench-matrix`:

- Runs the Baseline Matrix by default.
- Expands the exhaustive Full Matrix only with `--full`.
- Starts every scenario as an independent `aoi-bench` child process.
- Applies a configurable per-scenario timeout with a 15-minute default.
- Monitors child RSS externally and applies a Memory Guard defaulting to 75% of
  physical memory.
- Accepts `--max-rss` to override or disable the Memory Guard.
- Executes scenarios in ascending Observer-count order.
- Continues after child failure.
- Captures exit code, termination signal, elapsed time, and stderr summary.
- Writes aggregated JSON and CSV.

Process isolation prevents:

- A failed million-player scenario from losing smaller results.
- Heap capacity retained by one scenario from affecting the next.
- GC history from contaminating another scenario.

The Baseline Matrix contains:

- 100,000 Observers / 10,000 movers / normal.
- 1,000,000 Observers / 10,000 movers / normal.
- 1,000,000 Observers / 50,000 movers / sparse, normal, and hotspot.
- One Build scenario for each scale and density represented above, with three
  Scenario Repeats per scenario.

The Full Matrix includes every frozen scale, applicable mover count, density,
and mode combination. Every measured Build, Core, and World-plus-AOI scenario
has three process-isolated Scenario Repeats in both matrices. A failed scenario
or repeat does not suppress larger scenarios because the failure itself is
capacity-boundary evidence.

Each repeat preserves its own Tick median, P95, P99, and maximum. Aggregation
also reports min, median, and max across the three per-repeat medians and across
the three peak-RSS values. Failed repeats remain individual records and are not
hidden by successful repeats.

Profile executions are separately marked and excluded from ordinary Scenario
Repeat statistics.

## Failure Results

Every requested matrix scenario produces a result record with one status:

- `success`
- `timeout`
- `memory_limit`
- `oom`
- `signal`
- `runtime_error`
- `not_applicable`

Failure records include, where available:

- Scenario identity.
- Last known phase: generation, Build, warm-up, or measured Ticks.
- Wall-clock time before failure.
- Exit code.
- Termination signal.
- Configured Memory Guard threshold.
- Stderr summary.
- Partial metrics emitted before failure.

`memory_limit` means the matrix parent terminated the child after observed RSS
crossed the Memory Guard. `oom` is used only when the platform exposes reliable
evidence of an operating-system OOM termination. Other externally signaled
termination is recorded as `signal`; it is not guessed to be OOM.

The Memory Guard does not configure Go's runtime memory limit or GC behavior.
Its polling and termination overhead occur in the parent and are excluded from
child Tick timing.

The matrix runner must continue after a failed scenario.

## Go Benchmark Integration

Standard `testing.B` benchmarks use the same frozen workload definitions for:

- AOIIndex Build.
- AOIIndex steady-state Move.
- World plus AOI simulation.

They support:

```bash
go test -bench . -benchmem
go test -bench <name> -cpuprofile cpu.pprof
go test -bench <name> -memprofile heap.pprof
```

The Go benchmark layer is intended for:

- Fast local iterations.
- `benchstat` comparisons in the later optimization phase.
- Standard CPU and heap profiling.

Large matrix execution remains the responsibility of the standalone commands.

## Profiling

All scenarios record normal structured metrics. Raw CPU and heap profiles are
generated only for representative scenarios:

- 100,000 Observers / 10,000 movers.
- 1,000,000 Observers / 10,000 movers.
- 1,000,000 Observers / 50,000 movers.

If a representative scenario cannot complete, the failure is recorded and any
valid partial profile may be retained locally.

Raw `.pprof` files are not committed. The report commits:

- `go tool pprof` text summaries.
- Top CPU functions.
- Top allocation and in-use-space functions.
- Observed GC contribution.
- Candidate optimization hypotheses.

No hypothesis is implemented in this baseline phase.

## Memory Measurement

The runner records both Go runtime memory and operating-system process memory.
It also records workload heap after Compact Movement Schedule construction so
driver data remains distinguishable from World and AOI state.

Ordinary Scenario Repeats are Natural GC Runs. They do not call `runtime.GC`,
disable GC, or override `GOGC` or `GOMEMLIMIT`. Heap and GC state are recorded
at phase boundaries, and the warm-up lets the runtime respond naturally to the
workload.

Profile executions use the same GC configuration. If a profile collection API
requires an explicit GC, that execution records the fact and is excluded from
ordinary performance statistics.

Go metrics include:

- `HeapAlloc`
- `HeapInuse`
- `HeapObjects`
- `TotalAlloc`
- Allocation count when available from the benchmark path.
- `NumGC`
- Total GC pause.
- Longest observed GC pause.

Operating-system metrics include peak RSS.

Mac and Linux use platform-appropriate RSS collection and normalize the result
to bytes. The report records the collection source because platform semantics
may differ.

## Environment Metadata

Every result includes:

- Timestamp.
- Scenario seed and configuration.
- Git commit SHA and dirty-worktree flag.
- Go version.
- GOOS and GOARCH.
- Operating-system description.
- CPU model and logical CPU count when available.
- Total system memory when available.
- `GOMAXPROCS`.
- Relevant Go runtime environment such as `GOGC` and `GOMEMLIMIT`.
- Command-line arguments and timeout.

Results from different environments are never presented as directly
interchangeable without their metadata.

Each environment uses its default effective `GOMAXPROCS`, which is recorded but
not overridden. The benchmark is a Serial Core Baseline: World and AOI work is
serial even though Go's runtime, garbage collector, and operating system may
use other cores. Mac and Linux results describe their own capacity curves and
are not presented as a multi-core scaling comparison.

Reproducibility means the same commit, Go version, dependency versions,
arguments, and seed regenerate an equivalent logical workload. Cross-Go-version
byte identity is not promised. This phase does not introduce a custom PRNG,
workload schema version, or workload hash.

The runner may execute against a dirty worktree for local exploration, but a
baseline committed to the public report must come from a clean recorded commit.

## Result Artifacts

The repository contains:

- Benchmark source and tests.
- Frozen scenario configuration.
- Reproduction commands.
- Aggregated JSON and CSV baseline output.
- `docs/benchmarks/aoi-core-baseline.md`.

The report contains:

- Workload semantics and limitations.
- Mac hardware and software metadata.
- Build curves.
- Core Tick curves.
- World plus AOI curves.
- Memory and GC curves.
- Sparse, normal, and hotspot comparisons.
- Failure records.
- Profile summaries.
- An explicit distinction between logical Observers and real WebSocket
  connections.
- Logical Core Capacity Baseline terminology rather than online-player or
  production-capacity claims.
- Candidate optimization hypotheses for the next design.
- A reserved section requiring the Full Matrix for 32 vCPU / 64 GB Linux
  results.

The report summary is limited to Primary Metrics:

- Tick median, P95, P99, and maximum.
- Moves per second.
- Peak RSS and Go heap.
- GC pause.
- Visibility Churn.

Diagnostic Metrics remain in detailed result tables and profile analysis:

- Candidate checks.
- Exact distance checks.
- Allocation detail.
- Build checkpoints.
- Other counters used to explain Primary Metric changes.

Diagnostic Metrics are not separate capacity claims or acceptance thresholds.

The current Mac Baseline Matrix is committed during this phase. Running the
Full Matrix on Mac is optional exploration and is not a completion condition.
The target 32 vCPU / 64 GB Linux results run the Full Matrix and are appended
later using the same commit or a clearly recorded later commit and identical
workload configuration.

Performance numbers are experimental evidence, not CI assertions.

## Verification

### Workload Tests

- Under the same recorded commit and environment, the same seed produces
  identical IDs, positions, density assignments, active-player selections,
  Core trajectories, and World input schedules.
- Build Order is a deterministic permutation reused by all Scenario Repeats.
- Generation is independent from package-global randomness and map iteration
  order.
- Different seeds produce different perturbations while preserving scenario
  constraints.
- Sparse, normal, and hotspot distributions achieve their documented Initial
  Density ranges on deterministic samples.
- Steady-state Density is recorded after warm-up without being validated
  against the Initial Density ranges.
- Exactly 1% of players are assigned to hotspots when the scale permits.
- Movement Patterns contain the exact 80% / 20% proportions.
- Generated Core trajectories perform the documented Movement Patterns.
- Core and World schedules reach matching positions at every AOI Update
  boundary.
- Compact Movement Schedule expansion reuses buffers and occurs outside timed
  execution.
- Visibility Churn reports mean, P50, P95, and maximum entered-plus-left
  relationship counts per mover.
- Generated World schedules drive real movement through `ApplyInput` and
  `Step`.

### Metrics Tests

- Percentile calculations are correct for fixed samples.
- Primary and Diagnostic classification survives JSON and CSV serialization.
- Units are normalized consistently.
- AOI counters and relationship totals are correct in small known worlds.
- Heap and GC deltas do not underflow.
- Ordinary runners do not invoke explicit GC or mutate GC settings.
- RSS values are normalized to bytes.
- Environment metadata records required fields when the platform exposes them.

### Runner Tests

- Small Core Build and Tick scenarios complete successfully.
- Small World plus AOI scenarios use real World movement and update AOI every
  second simulation Tick.
- Generation and the first 20 AOI Updates are excluded from measured Core Tick
  samples.
- The first 40 World simulation Ticks and their 20 AOI Updates are excluded
  from measured World-plus-AOI samples.
- Build and steady-state metrics are reported separately.
- Optional profiles are created only when requested.

### Matrix Tests

- Every scenario runs in its own process.
- Default expansion contains exactly the Baseline Matrix.
- `--full` contains every frozen applicable scenario.
- Every measured scenario expands to three process-isolated Scenario Repeats.
- The default per-scenario timeout is 15 minutes and supports override.
- The Memory Guard defaults to 75% of physical memory and supports override or
  disablement.
- Scenarios are ordered by ascending Observer count.
- Successful child output is parsed and aggregated.
- Timeout produces a `timeout` record and the matrix continues.
- Memory Guard termination produces a `memory_limit` record.
- Reliably identified operating-system OOM produces an `oom` record.
- Non-zero exit produces a `runtime_error` record.
- Other signal termination produces a `signal` record.
- Inapplicable scale/mover combinations produce `not_applicable`.
- JSON and CSV contain matching scenario records.
- Repeat aggregation reports min, median, and max for per-repeat medians and
  peak RSS.
- A failed repeat remains visible beside successful repeats.
- Profile executions are excluded from ordinary repeat aggregation.

### Project Verification

Million-player experiments are not part of normal `go test ./...`.

Normal verification remains:

```bash
go test ./...
go vet ./...
```

The Mac baseline matrix is run explicitly and its commands are recorded in the
report.

## Acceptance Criteria

The phase is complete when:

- Frozen deterministic sparse, normal, and hotspot workloads exist.
- AOIIndex Core and World plus AOI are measured separately.
- Build and steady-state execution are measured separately.
- The frozen Full Matrix covers 100,000, 500,000, and 1,000,000 logical
  Observers with 10,000 and 50,000 movers where applicable.
- Every matrix scenario runs in an independent process.
- Time, AOI, heap, allocation, GC, and peak RSS metrics are recorded.
- Failure scenarios remain visible in structured output.
- Standard Go benchmarks support `benchmem` and profile generation.
- Representative CPU and heap profiles have committed text summaries.
- A reproducible Mac Baseline Matrix report is committed.
- The report clearly states that Hub and real connections remain unmeasured.
- End-to-end online capacity remains deferred until Hub, encoding, send queues,
  and real WebSocket connections are measured.
- Multi-core scaling remains deferred until Shards or parallel World/AOI
  execution exist.
- No profile-driven AOI optimization is included in this phase.
- Existing AOI behavior remains unchanged.
- `go test ./...` passes.
- `go vet ./...` passes.
