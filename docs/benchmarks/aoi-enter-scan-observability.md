# AOI Enter Scan Observability Benchmark

Date: 2026-06-23
Spec: docs/superpowers/specs/2026-06-23-aoi-enter-scan-observability-workload-design.md
Plan: docs/superpowers/plans/2026-06-23-aoi-enter-scan-observability-workload.md

## Commands

```
go test ./internal/realtime -run '^$' -bench BenchmarkHubReplicationRandomJump -benchmem
go test ./internal/realtime -run '^$' -bench BenchmarkHubReplicationContinuousMove -benchmem
```

## HubReplication 2000 clients: RandomJump vs ContinuousMove

| metric | RandomJump (worst-case) | ContinuousMove | delta |
|---|---|---|---|
| ns/op | 13,945,000 | 4,940,000 | **2.8x faster** |
| aoi_move_us/op | 6,282 | 1,304 | **4.8x faster** |
| candidate_pairs/op | 68,571 | 4,053 | **16.9x fewer** |
| distance_checks/op | 96,279 | 28,325 | **3.4x fewer** |
| full_enter_scans/op | 1,600 | 91 | 94% fewer |
| skipped_enter_scans/op | 0 | 1,509 | — |
| **enter_scan_skip_rate** | **0** | **0.94** | — |
| leave_checks/op | 27,694 | 24,272 | ~same |
| entered/op | 2,243 | 0 | stabilized |
| left/op | 2,229 | 0 | stabilized |
| moved/op | 1,600 | 1,600 | same |
| msgs/op | 1,994 | 2,000 | ~same |
| bytes/op | 17,190,000 | 7,193,000 | **2.4x fewer** |
| allocs/op | 80,923 | 46,302 | **1.7x fewer** |

> RandomJump: 每轮玩家随机跳位置（Speed=3000m/s），必定换 cell → 0% skip.
> ContinuousMove: 玩家按方向连续移动（Speed=600m/s, 30m/tick），
> 94% 的移动无需九格 enter 扫描 → candidate pairs 从 68k 降到 4k。

## Workload Interpretation

- **RandomJump** 是 AOI 全扫描上限压力测试，不代表真实玩家行为。
- **ContinuousMove** 模拟连续方向输入，接近实际游戏负载模式。
- 两个 benchmark 都应关注：RandomJump 用于容量规划上限，
  ContinuousMove 用于评估 enter-scan 优化在生产中的实际收益。

## Online Tick Interpretation

Simulation ticks 目标为 20/秒（20Hz）。1s stats 窗口内观测到 19-21 tick
属于正常抖动范围——可能来自调度器抖动、GC、actor 工作跨越 stats 窗口边界。

能独立构成容量告警的信号：
- 持续 `simulation_ticks < 19`
- broadcast cadence 低于 10 Hz
- dispatcher queue depth 或 drops 持续上升
- AOI detailed move 耗时上升
- continuous movement 下 enter-scan skip rate 异常偏低
- WebSocket send failures
- GC pauses 或 goroutine 堆积

单独一个指标在边界值附近不应触发告警；多个信号同时出现时才需要调查。

## Decision

After this phase:

- [x] Continuous benchmark shows high skip rate (94%) AND online skip rate matches.
  → Keep current incremental enter-scan algorithm; `EnterRescanDistanceMeters=50`
    is effective for continuous movement.

Enter-scan 优化在连续移动场景显著降低 AOI 开销（AOI 耗时降至 1/5，
candidate pairs 降至 1/17）。下一个优化方向应关注 leave checks（24k/op，
在两种 workload 中均占 AOI 距离检查的 25%+）或 collectible visibility。
