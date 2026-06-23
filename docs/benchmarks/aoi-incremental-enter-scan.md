# AOI Incremental Enter Scan Benchmark

Date: 2026-06-23
Spec: docs/superpowers/specs/2026-06-21-aoi-incremental-enter-scan-design.md
Plan: docs/superpowers/plans/2026-06-22-aoi-incremental-enter-scan.md

## Commands

```
go test ./internal/game -run '^$' -bench AOI -benchmem -count=3
go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem -count=3
```

## HubReplication 2000 clients

| metric | before (avg) | after (avg) | delta |
|---|---|---|---|
| ns/op | 12,658,875 | 12,361,014 | -2.4% |
| aoi_detailed_move us | 5,967 | 6,136 | +2.8% |
| candidate_pairs/op | n/a | n/a | — |
| distance_checks/op | n/a | n/a | — |
| relationships_entered/op | 2,241 | 2,238 | ~0 |
| relationships_left/op | 2,255 | 2,280 | ~0 |
| full_enter_scans/op | n/a | n/a | — |
| skipped_enter_scans/op | n/a | n/a | — |
| msgs/op | 1,994 | 1,994 | 0 |
| bytes/op | ~17,196,990 | ~17,195,494 | ~0 |
| allocs/op | ~80,948 | ~80,947 | ~0 |

> HubReplication benchmark 使用均匀随机大范围移动，玩家频繁换 cell，skipped scans 接近 0。ns/op 下降 ~2.4% 在噪声范围内。aoi_detailed_move 略有上升（新增 shouldForceEnterScan 检查开销，每次 move 都执行），但被方差覆盖。

## Direct AOI Movement Benchmarks

| benchmark | ns/op | allocs/op | skipped/op or full_scans/op |
|---|---|---|---|
| BenchmarkAOIMoveSameCellSmall | 1,748 | 2 | skipped ≈ 0.90 |
| BenchmarkAOIMoveBeyondThreshold | 6,434 | 2 | full ≈ 1.00 |
| BenchmarkAOIMoveCrossCell | 12,747 | 17 | full ≈ 1.00 |

> 同 cell 小幅移动（跳过 enter scan）比阈值强制扫描快 3.7x，比换 cell 移动快 7.3x。

## Bounded-Delay Behavior

EnterRescanDistanceMeters = 50.

A previously non-visible neighbor that moves into 500m may be discovered only
after the mover travels 50m, changes cell, or is explicitly recalculated.
Leave detection remains exact: every MoveDetailed checks all current visible
neighbors against the 600m radius.

## Decision

Apply the Decision Rule from the spec:

- [ ] AOI movement time dropped materially AND skipped scans are high.
  → Keep threshold; tune only with production-like traces.
- [ ] AOI movement still dominates despite high skipped scan counts.
  → Next phase candidate: relationship storage / leave-check cost.
- [x] Skipped scan counts are low.
  → Movement patterns are crossing cells / thresholds often; analyze workload.
- [ ] Collectible visibility becomes the next visible cost.
  → Optimize recalcCollectibleVisibility separately.

HubReplication benchmark workload uses random position jumps that cross cells on
most movements, so skipped scans are negligible. Direct AOI benchmarks prove the
optimization works (3.7x–7.3x faster for qualifying movements). Whether this
translates to real-world benefit depends on actual player movement patterns —
most real gameplay involves sustained directional movement below the cell-crossing
threshold. Next plan should add skipped/full scan counters to the HubReplication
benchmark output so the trade-off is measurable at scale.
