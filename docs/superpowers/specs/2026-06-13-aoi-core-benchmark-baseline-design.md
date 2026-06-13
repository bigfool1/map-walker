# AOI Core Benchmark Baseline Design

Date: 2026-06-13

## Goal

Build a reproducible performance baseline for the current AOI and authoritative
movement implementation before making performance optimizations.

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
- Structured failure results for timeout, OOM/signal, and runtime errors.

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
precomputed player positions
  -> AOIIndex.Insert for every player
  -> Build metrics
```

Steady-state Tick path:

```text
precomputed destination positions
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

- Grid spacing controls the target visible-neighbor density.
- A fixed random seed controls perturbation and movement selection.
- Generation completes before timed work.
- The generator validates the achieved density on deterministic samples and
  records the observed relationship counts.

The benchmark uses Shanghai-local coordinates through the existing
`game.AOIConfig`. No map provider or geographic dataset is loaded.

### Density Scenarios

#### Sparse

Target approximately 10 visible neighbors per player.

This scenario shows index and player-state overhead when adjacency is small.

#### Normal

Target approximately 50 visible neighbors per player.

This is the primary one-million-online capacity workload.

#### Hotspot

- 99% of players use the normal distribution.
- 1% occupy deterministic high-density regions.
- Players in the hotspot target approximately 200-500 visible neighbors.
- The hotspot must not collapse all players into one fully connected region.

This scenario measures uneven urban density and adjacency-tail costs.

### Movement Mix

Moving-player trajectories are generated before timed execution:

- 80% perform small movement that remains within the current Cell.
- 15% cross a Cell boundary.
- 5% cross an AOI enter or leave threshold.

The selection and trajectory schedule use a fixed seed. Random-number
generation, position planning, and trajectory classification are excluded from
timed sections.

For AOIIndex Core, the generated destinations are passed directly to
`AOIIndex.Move`.

For World plus AOI, initial placement and direction schedules are arranged so
real `ApplyInput` and `Step` movement exercise the same workload classes.
World positions are never directly mutated by the benchmark.

## Build Measurement

Build is measured separately from steady-state movement because Shard startup
or recovery has different requirements from normal Tick processing.

For each scale and density:

- Start from an empty AOIIndex.
- Insert players sequentially through `AOIIndex.Insert`.
- Run the Build scenario in an independent process.
- Repeat the Build three times.

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
- Success or structured failure.

Relationship counting may occur after the timed Build region if obtaining the
count requires a full traversal.

World plus AOI setup is also measured and reported separately:

- Time to add all World players.
- Time to build AOI membership and relationships.
- World and AOI memory after setup.

## AOIIndex Steady-State Measurement

Each Core Tick scenario:

1. Builds the complete AOIIndex before timing.
2. Executes 20 warm-up Ticks.
3. Executes 100 measured Ticks.
4. Applies 10,000 or 50,000 precomputed moves per Tick.

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

Each World plus AOI scenario measures 200 simulation Ticks, representing 10
seconds of game time:

- Every Tick executes `World.Step(50ms)`.
- Every second Tick consumes moved player IDs and updates AOIIndex.
- Active-player direction changes use the precomputed schedule and real
  `World.ApplyInput`.

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

- Expands the frozen matrix.
- Starts every scenario as an independent `aoi-bench` child process.
- Applies a configurable per-scenario timeout.
- Continues after child failure.
- Captures exit code, termination signal, elapsed time, and stderr summary.
- Writes aggregated JSON and CSV.

Process isolation prevents:

- A failed million-player scenario from losing smaller results.
- Heap capacity retained by one scenario from affecting the next.
- GC history from contaminating another scenario.

## Failure Results

Every requested matrix scenario produces a result record with one status:

- `success`
- `timeout`
- `oom_or_signal`
- `runtime_error`
- `not_applicable`

Failure records include, where available:

- Scenario identity.
- Last known phase: generation, Build, warm-up, or measured Ticks.
- Wall-clock time before failure.
- Exit code.
- Termination signal.
- Stderr summary.
- Partial metrics emitted before failure.

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
- Candidate optimization hypotheses for the next design.
- A reserved section for 32 vCPU / 64 GB Linux results.

The current Mac baseline is committed during this phase. The target Linux
results are appended later using the same commit or a clearly recorded later
commit and identical workload configuration.

Performance numbers are experimental evidence, not CI assertions.

## Verification

### Workload Tests

- The same seed produces identical IDs, positions, density assignments,
  active-player selections, Core trajectories, and World input schedules.
- Different seeds produce different perturbations while preserving scenario
  constraints.
- Sparse, normal, and hotspot distributions achieve their documented density
  ranges on deterministic samples.
- Exactly 1% of players are assigned to hotspots when the scale permits.
- Movement classes contain the exact 80% / 15% / 5% proportions.
- Generated Core trajectories perform the documented movement classes.
- Generated World schedules drive real movement through `ApplyInput` and
  `Step`.

### Metrics Tests

- Percentile calculations are correct for fixed samples.
- Units are normalized consistently.
- AOI counters and relationship totals are correct in small known worlds.
- Heap and GC deltas do not underflow.
- RSS values are normalized to bytes.
- Environment metadata records required fields when the platform exposes them.

### Runner Tests

- Small Core Build and Tick scenarios complete successfully.
- Small World plus AOI scenarios use real World movement and update AOI every
  second simulation Tick.
- Generation and warm-up are excluded from measured Core Tick samples.
- Build and steady-state metrics are reported separately.
- Optional profiles are created only when requested.

### Matrix Tests

- Every scenario runs in its own process.
- Successful child output is parsed and aggregated.
- Timeout produces a `timeout` record and the matrix continues.
- Non-zero exit produces a `runtime_error` record.
- Signal termination produces an `oom_or_signal` record.
- Inapplicable scale/mover combinations produce `not_applicable`.
- JSON and CSV contain matching scenario records.

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
- The scale matrix covers 100,000, 500,000, and 1,000,000 logical online
  Observers with 10,000 and 50,000 movers where applicable.
- Every matrix scenario runs in an independent process.
- Time, AOI, heap, allocation, GC, and peak RSS metrics are recorded.
- Failure scenarios remain visible in structured output.
- Standard Go benchmarks support `benchmem` and profile generation.
- Representative CPU and heap profiles have committed text summaries.
- A reproducible Mac baseline report is committed.
- The report clearly states that Hub and real connections remain unmeasured.
- No profile-driven AOI optimization is included in this phase.
- Existing AOI behavior remains unchanged.
- `go test ./...` passes.
- `go vet ./...` passes.
