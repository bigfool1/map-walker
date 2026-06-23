# AOI Enter Scan Observability And Workload Design

2026-06-23

## Problem

The incremental enter-scan optimization is effective in direct AOI benchmarks:

```text
BenchmarkAOIMoveSameCellSmall   ~1.8us   skip scan
BenchmarkAOIMoveBeyondThreshold ~6.9us   full scan
BenchmarkAOIMoveCrossCell       ~13.4us  full scan + cell move
```

However, `BenchmarkHubReplication` still reports `skipped scans = 0` because
its current workload jumps or moves in a pattern that nearly always forces full
enter scans. That benchmark is useful as a worst-case cross-cell/full-scan
stress test, but it does not represent continuous player movement.

Without a continuous movement workload and an explicit skip-rate metric, the
project cannot tell whether production resembles:

- continuous movement where same-cell small moves dominate, or
- random-jump movement where fast path eligibility is rare.

## Goal

Add observability and benchmark coverage that quantify AOI enter-scan fast-path
eligibility:

- preserve the existing worst-case Hub replication benchmark
- add a continuous movement Hub replication benchmark
- expose enter scan skip rate through stats snapshots and benchmark reports
- document how to interpret 19-21 simulation ticks per second under load

## Non-Goals

- Do not change AOI enter-scan algorithm behavior.
- Do not change `EnterRescanDistanceMeters`.
- Do not change movement speed, AOI geometry, or replication protocol.
- Do not optimize collectible visibility in this phase.
- Do not add per-player stats or per-cell stats.
- Do not compute stats by scanning AOI maps from HTTP handlers.

## Stats Semantics

Existing raw counters should remain the source of truth:

```text
AOIFullEnterScans
AOISkippedEnterScans
AOILeaveChecks
AOIStableRelationships
AOICandidatePairs
AOIDistanceChecks
```

Add or document a derived metric:

```text
enter_scan_skip_rate =
  AOISkippedEnterScans / (AOIFullEnterScans + AOISkippedEnterScans)
```

If the denominator is zero, the skip rate should be reported as zero or omitted
with clear JSON behavior. The implementation plan should choose one consistent
representation.

## Stats Pressure

This metric must follow the existing snapshot model:

```text
AOI hot path:
  increment raw uint64 counters during MoveDetailed

Hub.logStats once per second:
  aoiStats := h.aoi.TakeStats()
  compute or store skip rate in HubSnapshot

/api/stats/synthetic:
  return the latest immutable snapshot
```

The HTTP stats handler must not:

- traverse `AOIIndex.players`
- traverse `AOIIndex.cells`
- traverse `AOIIndex.visible`
- calculate per-player values
- call live AOI methods

Under this model, `/stats` pressure is negligible compared with existing AOI
counters because API requests only serialize a previously published snapshot.

## Benchmark Workloads

Keep the current benchmark as a worst-case workload and rename or label it
explicitly:

```text
BenchmarkHubReplicationRandomJump
```

Add a continuous movement workload:

```text
BenchmarkHubReplicationContinuousMove
```

Continuous movement should:

- use deterministic initial placement
- keep players in the same world and AOI index across iterations
- apply directional input over repeated simulation ticks
- avoid resetting or jumping positions between iterations
- use realistic movement distance from the configured speed and tick interval
- run enough warm-up ticks to establish AOI relationships
- report the same replication counters as the existing benchmark

Both benchmark variants must report:

```text
full_enter_scans/op
skipped_enter_scans/op
enter_scan_skip_rate
leave_checks/op
candidate_pairs/op
distance_checks/op
aoi_move_us/op
msgs/op
bytes/op
moved/op
entered/op
left/op
```

## Online Interpretation

Simulation ticks are scheduled at 20 Hz, so a one-second snapshot near 20 ticks
is nominally healthy.

Observed `simulation_ticks` fluctuating around 19-21 at 1.8k online users is a
pressure signal, but not by itself proof of overload:

- 20 is the target.
- 19 can happen from scheduler jitter, GC, actor work spilling past the exact
  one-second stats window, or occasional tick overrun.
- 21 can happen when stats windows do not align perfectly with ticker delivery.

Treat 19-21 as "near budget, watch closely" rather than immediate failure.

It becomes a capacity warning when paired with one or more of:

- sustained `simulation_ticks < 19`
- broadcast tick rate dropping below its expected 10 Hz cadence
- increasing actor handoff latency
- increasing dispatcher queue depth or drops
- rising AOI detailed move duration
- low enter-scan skip rate under continuous movement
- WebSocket send failures from backpressure
- GC pauses or goroutine buildup

The stats page/report should phrase this as:

```text
19-21 simulation ticks/s: near target with jitter.
Sustained below 19 or paired with rising queues/durations: capacity pressure.
```

## Decision Rule

After this phase:

- If continuous benchmark and online stats both show high skip rate and tick
  cadence improves, keep the current incremental enter-scan algorithm.
- If continuous benchmark shows high skip rate but online stats show low skip
  rate, investigate synthetic/mock movement realism and production movement
  patterns.
- If skip rate is high but AOI movement still dominates, investigate leave
  checks or relationship storage.
- If skip rate is low because players often cross cells or exceed threshold,
  analyze cell geometry, movement speed, and `EnterRescanDistanceMeters` before
  changing the algorithm.
- If collectible visibility becomes visible after AOI movement improves, plan
  `recalcCollectibleVisibility` optimization separately.

## Tests

Required tests:

- Stats snapshot exposes raw enter-scan counters.
- Stats snapshot or report exposes `enter_scan_skip_rate`.
- `/api/stats/synthetic` returns the latest snapshot without traversing AOI.
- Existing stats API tests still pass.
- Benchmark output includes full/skipped scan counts and skip rate for both
  random-jump and continuous movement variants.

## Follow-Up Path

This phase is observability and workload calibration only:

```text
Current:
direct AOI benchmark proves fast path
HubReplication benchmark does not exercise fast path

This phase:
worst-case Hub benchmark remains
continuous movement Hub benchmark measures realistic skip rate
stats expose skip rate online
```

Do not start the next AOI algorithm change until the continuous benchmark and
online skip-rate data explain whether production benefits from the current
fast path.
