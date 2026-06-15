# MySQL Position Batch Persistence Benchmark

Date: 2026-06-15

## Environment

| Item | Value |
|------|-------|
| MySQL | 9.3.0 |
| Go | go1.26.3 |
| GOARCH | arm64 |
| GOOS | darwin |
| CPU | Apple M5 |
| Command | `go test -run '^$' -bench 'BenchmarkPositionPersistence' -benchmem -count=5 ./internal/storage` |

## Results

### 1,000 Position Updates

| Run | Baseline (per-row) | Optimized (chunked) |
|-----|-------------------|---------------------|
| 1 | 159.3ms | 9.5ms |
| 2 | 159.5ms | 9.0ms |
| 3 | 167.6ms | 9.0ms |
| 4 | 161.2ms | 9.9ms |
| 5 | 154.6ms | 9.1ms |
| **Avg** | **160.4ms** | **9.3ms** |

- **Speedup: 17.3x**
- Database calls: 1,000 → 2 chunks
- Allocations: 560KB → 662KB

### 4,000 Position Updates

| Run | Baseline (per-row) | Optimized (chunked) |
|-----|-------------------|---------------------|
| 1 | 616.6ms | 36.5ms |
| 2 | 557.7ms | 36.8ms |
| 3 | 627.9ms | 39.5ms |
| 4 | 655.4ms | 40.7ms |
| 5 | 652.7ms | 40.7ms |
| **Avg** | **622.1ms** | **38.8ms** |

- **Speedup: 16.0x**
- Database calls: 4,000 → 8 chunks
- Allocations: 2.24MB → 2.65MB

## Chunk Verification

| Batch Size | Expected Chunks | Actual Chunks |
|-----------|----------------|---------------|
| 1,000 | 2 | 2 |
| 4,000 | 8 | 8 |

## Conclusion

Chunked bulk persistence (`UPDATE ... JOIN`) reduces database round trips by 
500x (one statement per 500 users vs one per user), yielding a **16-17x wall-clock 
speedup** on MySQL 9.3. The per-row overhead of connection round trips and 
transaction management dominates at scale. Chunk count matches the expected 
`ceil(rows / 500)` formula.
