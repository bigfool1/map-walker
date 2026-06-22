# AOI Incremental Enter Scan Design

2026-06-21

## Problem

After replication offload, synchronous `ReplicationBuilder`, and AOI movement
delta plumbing, benchmark evidence still shows AOI movement as the dominant
actor-owned cost:

```text
HubReplication 2000 clients: ~13,540 us/op
AOI detailed move: ~6,591 us/op (~49%)
Collectible recalc: 0 in this benchmark
```

The current AOI movement path still performs a full enter-candidate scan for
each moved player:

```text
MoveDetailed
  -> recalculateRelationships
      -> scan 9 cells
      -> skip already visible
      -> distance-check candidates
      -> add newly entered relationships
      -> check existing visible neighbors for leave
```

With 1600 movers, this repeats the nine-cell candidate scan 1600 times per
broadcast. In typical movement, relationship churn is low: most moved players
remain visible to the same neighbors, and only a small fraction actually enter
or leave relationships on each tick.

## Goal

Reduce AOI movement cost by skipping full entered-candidate scans for small
same-cell movements while preserving exact leave detection and bounding delayed
enter discovery.

## Semantic Change

AOI enter discovery may be delayed within a configured movement threshold.

This is acceptable because AOI controls replication visibility, not collision,
combat, or authoritative interaction. The system already replicates at 10 Hz,
so visibility is not continuous-time exact.

Leave detection remains exact per movement update: currently visible neighbors
must still be checked against the 600m leave radius every AOI movement.

## Non-Goals

- Do not change 500m enter radius.
- Do not change 600m leave radius.
- Do not change relationship symmetry.
- Do not change cell size or cell membership rules.
- Do not change WebSocket protocol fields.
- Do not introduce shards or parallel AOI.
- Do not optimize collectible visibility in this phase.
- Do not replace relationship storage with dense IDs in this phase.

## Design

Add enter-scan state to each AOI player:

```go
type aoiPlayer struct {
    lat, lng       float64
    localX, localY float64
    cell           CellCoord

    lastEnterScanX float64
    lastEnterScanY float64
    lastEnterScanCell CellCoord
}
```

Add configuration:

```go
EnterRescanDistanceMeters float64
```

Default should be conservative, for example 50m. The exact value should live in
`AOIConfigFromWorld` and be documented as the maximum movement distance before
an enter scan is forced.

`MoveDetailed` should split AOI movement into two parts:

1. Always update position and cell.
2. Always check existing visible neighbors for leave radius.
3. Only run the full nine-cell enter scan when forced.

An enter scan is forced when any of these are true:

- player changed cell
- player has no prior enter-scan marker
- distance from last enter-scan position is greater than or equal to
  `EnterRescanDistanceMeters`
- caller explicitly requests a full recalculation path such as `Insert` or
  `RecalculateRelationships`

When a full enter scan runs, update the player's last enter-scan marker to the
current local position and cell.

## Movement Delta

`MovementDelta` remains the output shape:

```go
type MovementDelta struct {
    PlayerID int64
    Entered  []int64
    Left     []int64
    Stable   []int64
}
```

If an enter scan is skipped:

- `Entered` is empty.
- `Left` contains any relationships that crossed beyond 600m.
- `Stable` contains visible neighbors that remained after leave checks.

If an enter scan runs:

- `Entered` contains newly visible relationships.
- `Left` contains relationships that crossed beyond 600m.
- `Stable` contains neighbors that were visible before movement and remain
  visible after leave checks, excluding newly entered neighbors.

## Stats

Extend AOI stats with:

- full enter scans
- skipped enter scans
- leave checks
- stable relationships returned

Keep existing stats:

- candidate pairs
- distance checks
- relationships entered
- relationships left

The benchmark report must show whether performance improvement comes from fewer
candidate pairs and distance checks, not from changing replication output
accidentally.

## Correctness Requirements

Exact guarantees:

- Relationship symmetry is preserved.
- Existing visible neighbors are removed as soon as they exceed 600m on a move.
- Cell membership is updated on every move.
- Full enter scans still use the same 500m enter radius and nine-cell coverage.
- `Insert` establishes initial relationships immediately.
- `RecalculateRelationships` performs a full scan.

Bounded-delay guarantee:

- A previously non-visible neighbor that moves into 500m may be discovered only
  after the mover travels `EnterRescanDistanceMeters`, changes cell, or is
  explicitly recalculated.

This delay must be documented in tests and benchmark reports.

## Tests

Required AOI unit tests:

- Same-cell movement below threshold skips full enter scan.
- Same-cell movement below threshold still removes neighbors beyond 600m.
- Movement beyond threshold runs full enter scan.
- Cell change runs full enter scan.
- `Insert` establishes relationships immediately.
- `RecalculateRelationships` runs a full enter scan.
- Delayed enter behavior is explicit: a newly within-500m neighbor may not
  enter until threshold movement or cell change.
- Symmetry is preserved after skipped scans, full scans, enters, and leaves.
- AOI stats count full enter scans and skipped enter scans.

Required realtime tests:

- Movement deltas still produce correct stable position fanout.
- Enter events can be delayed only in the allowed same-cell below-threshold
  scenario.
- Leave events are not delayed.
- Existing replication benchmark message/byte counts remain logically
  comparable, with documented differences if delayed enter changes timing.

## Benchmarks

Run:

```bash
go test ./internal/game -run '^$' -bench AOI -benchmem
go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem
```

Compare before and after:

- HubReplication ns/op
- AOI detailed move duration
- candidate pairs
- distance checks
- full enter scans
- skipped enter scans
- relationships entered/left
- replication messages
- replication bytes
- allocations

## Decision Rule

After this phase:

- If AOI movement time drops materially and skipped enter scans are high, keep
  the threshold and tune it only with production-like movement traces.
- If AOI movement still dominates despite high skipped scan counts, relationship
  storage or leave-check cost is likely next.
- If skipped scan counts are low, movement patterns are crossing cells or
  thresholds too often; consider cell geometry or workload-specific analysis.
- If collectible visibility becomes the next visible cost, optimize
  `recalcCollectibleVisibility` separately.

## Follow-Up Path

This phase introduces bounded-delay enter discovery:

```text
Current:
every mover -> full nine-cell enter scan + leave checks

This phase:
every mover -> leave checks
eligible movers -> full nine-cell enter scan
small same-cell movers -> skip enter scan
```

Do not proceed to dense relationship storage, shard actors, or collectible
visibility optimization until this phase has a benchmark report with AOI
candidate/distance counts and skipped/full scan counts.
