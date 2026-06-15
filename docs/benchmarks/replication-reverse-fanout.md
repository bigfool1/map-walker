# Replication Reverse Fan-Out — Performance Comparison

Date: 2026-06-15

## Environment

- **Machine:** Apple M5, Darwin arm64
- **Go version:** go1.26.3 darwin/arm64
- **Baseline commit:** pre-optimisation (all-client-by-all-mover scan)
- **Optimised commit:** post reverse fan-out (recipient accumulation)

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

## Results

### 2,000 clients (1,600 moved)

| Metric | Baseline | Optimised | Change |
|--------|----------|-----------|--------|
| ns/op | ~97,013,226 | ~15,278,300 | **-84.3%** |
| ms/op | ~97.0 | ~15.3 | **6.3× faster** |
| msgs/op | 2,000 | 2,000 | identical |
| bytes/op | 2,906,450 | 2,906,450 | identical |
| moved/op | 1,600 | 1,600 | identical |
| entered/op | 1,655 | 1,655 | identical |
| left/op | 1,584 | 1,584 | identical |

### 3,000 clients (2,400 moved)

| Metric | Baseline | Optimised | Change |
|--------|----------|-----------|--------|
| ns/op | ~211,713,458 | ~34,440,833 | **-83.7%** |
| ms/op | ~211.7 | ~34.4 | **6.1× faster** |
| msgs/op | 3,000 | 3,000 | identical |
| bytes/op | 6,864,804 | 6,864,804 | identical |
| moved/op | 2,400 | 2,400 | identical |
| entered/op | 3,612 | 3,612 | identical |
| left/op | 3,497 | 3,497 | identical |

## Remaining Bottlenecks

- **JSON encoding** (2000–3000 messages, 3–7 MB per broadcast) dominates the remaining ~15–34 ms.
- **Channel send/drain** for thousands of clients adds overhead.
- **Benchmark-only:** the measured code runs synchronously. Real-world Hub select loop adds goroutine scheduling and channel multiplexing overhead.

These are follow-up evidence for subsequent phases (encoding, transport, persistence). They are not addressed in this implementation.

## Notes

- `B/op` and `allocs/op` from `-benchmem` reflect only the benchmark goroutine, not Hub internal allocations. Absolute allocation comparison is not meaningful.
- ns/op includes World.Step, broadcastReplication, JSON encoding, channel send, and drain. It excludes HTTP, WebSocket framing, kernel buffers, persistence, and frontend rendering.
