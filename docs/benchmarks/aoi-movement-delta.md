# AOI Movement Delta — Benchmark Evidence

2026-06-21

## Commands

```bash
go test ./internal/realtime -run '^$' -bench 'Benchmark(HubReplication|ReplicationBuilder)' -benchmem -count=1
```

## Headline Results (Apple M5, Go 1.26)

### Hub Replication (end-to-end: world.Step + broadcastReplication + dispatcher submit)

| Clients | ops/s | ns/op | msgs/op | bytes/op | B/op | allocs/op |
|---------|-------|-------|---------|----------|------|-----------|
| 2000 | 100 | 12,739,791 | 1994 | 3,018,468 | 17.2M | 80,972 |
| 3000 | 46 | 25,506,525 | 2581 | 5,987,912 | 38.4M | 128,225 |

### Builder (fanout-only, movement deltas as input)

| Variant | Recipients | ns/op | jobs/op | accum µs | copy µs | total µs | B/op | allocs/op |
|----------|------------|-------|---------|----------|---------|----------|------|-----------|
| iface-1000 | 1000 | 366,126 | 1000 | 285 | 70 | 355 | 1.1M | 7,456 |
| concrete-1000 | 1000 | 360,237 | 1000 | 268 | 68 | 336 | 1.1M | 7,455 |
| iface-2000 | 2000 | 1,168,798 | 2000 | 1123 | 374 | 1498 | 3.2M | 17,141 |
| concrete-2000 | 2000 | 1,172,912 | 2000 | 899 | 162 | 1062 | 3.2M | 17,141 |

## Observations

- `snapshotMoverVisibility` 已从 `broadcastReplication` 热路径中删除
- `moverHadNeighbor` 已删除（此前未被任何代码调用）
- Builder 不再调用 `VisibleNeighbors` 做 stable 邻居扇出，直接从 `MovementDelta.Stable` 读取
- msgs/op、entered/op、left/op 与 replication-builder baseline 逻辑可比

## Next Bottleneck

根据 plan 的 decision rule，下一步方向取决于 profiling 结果：

- AOI detailed move duration 和 collectible recalc duration 现在有独立计时
- 如果 collectible recalc 主导剩余成本 → 优化 collectible visibility
- 如果 AOI candidate/distance 仍主导 → 设计 boundary-aware incremental AOI
