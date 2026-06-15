# Replication Reverse Fan-Out — Baseline

Date: 2026-06-15

## Environment

- **Machine:** Apple M5, Darwin arm64
- **Go version:** go1.26.3 darwin/arm64
- **Commit:** pre-optimisation (all-client-by-all-mover scan)

## Scenario

- 10km × 10km activity region, deterministic placement (same hash as synthetic/placement.go)
- 80% movement ratio, alternating Right/Left direction
- Speed 3000 m/s, 50ms simulation interval
- 500m enter / 600m leave hysteresis, 600m grid cells
- Direct Step + broadcastReplication call (no select-loop randomness)
- Bounded send queues (buffer 256), immediately drained

## Command

```
go test -run '^$' -bench 'BenchmarkHubReplication/(2000|3000)$' -benchmem -benchtime=1x -count=5 ./internal/realtime
```

## Baseline Results (pre-change)

### 2,000 clients (1,600 moved)

| Run | ns/op | msgs/op | bytes/op | moved/op | entered/op | left/op |
|-----|-------|---------|----------|----------|------------|---------|
| 1 | 97,415,584 | 2,000 | 2,906,450 | 1,600 | 1,655 | 1,584 |
| 2 | 93,349,833 | 2,000 | 2,906,450 | 1,600 | 1,655 | 1,584 |
| 3 | 96,801,541 | 2,000 | 2,906,450 | 1,600 | 1,655 | 1,584 |
| 4 | 93,997,543 | 2,000 | 2,906,450 | 1,600 | 1,655 | 1,584 |
| 5 | 103,501,625 | 2,000 | 2,906,450 | 1,600 | 1,655 | 1,584 |

**Average:** ~97.0 ms/op, 2,000 msgs/op, 2,906,450 bytes/op

### 3,000 clients (2,400 moved)

| Run | ns/op | msgs/op | bytes/op | moved/op | entered/op | left/op |
|-----|-------|---------|----------|----------|------------|---------|
| 1 | 209,642,707 | 3,000 | 6,864,804 | 2,400 | 3,612 | 3,497 |
| 2 | 210,502,209 | 3,000 | 6,864,804 | 2,400 | 3,612 | 3,497 |
| 3 | 207,087,000 | 3,000 | 6,864,804 | 2,400 | 3,612 | 3,497 |
| 4 | 218,015,292 | 3,000 | 6,864,804 | 2,400 | 3,612 | 3,497 |
| 5 | 213,320,083 | 3,000 | 6,864,804 | 2,400 | 3,612 | 3,497 |

**Average:** ~211.7 ms/op, 3,000 msgs/op, 6,864,804 bytes/op

## Logical Counter Stability

- moved/op, msgs/op, and bytes/op are identical across all 5 runs at each scale.
- The deterministic placement and direct method-call approach eliminate select-loop randomness.

## Notes

- `B/op` and `allocs/op` from `-benchmem` reflect only the benchmark goroutine, not Hub internal allocations. Pre/post comparison of allocation counts is not meaningful with this benchmark structure.
- ns/op includes World.Step, broadcastReplication, JSON encoding, channel send, and drain. It excludes HTTP, WebSocket framing, kernel buffers, persistence, and frontend rendering.
- The message count (2,000 / 3,000) equals the client count because every client either moves (receiving SelfPosition) or observes a moving neighbor. This is expected for the 10km×10km region at this client density.
