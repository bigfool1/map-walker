# AOI Core Logical Capacity Baseline

> Mac baseline report for the AOIIndex Core and World-plus-AOI benchmark.
> Full Matrix (500k, multi-density) deferred to Linux 32GB+ target.

**Generated:** 2026-06-14
**Machine:** Apple M5, 10 cores, 16GB RAM
**OS:** macOS (darwin/arm64)
**Go:** go1.26.3
**Commit:** 3af14009eb6d104496da69176fdf26a252c63e67 (dirty worktree — benchmark artifacts only)
**Seed:** 42
**Baseline Kind:** Serial Core Baseline

---

## 1. Reproduction

```sh
# Build the benchmark binary
go build -o /tmp/aoi-bench ./cmd/aoi-bench

# Run the Mac Baseline Matrix (from project root)
go run docs/benchmarks/run_baseline_matrix.go

# Profile a single scenario (example)
/tmp/aoi-bench -mode core_tick -scale 100000 -movers 10000 -density normal -seed 42 \
  -cpuprofile /tmp/cpu.pprof -memprofile /tmp/heap.pprof -repeat 1
go tool pprof -top /tmp/cpu.pprof
go tool pprof -top -inuse_space /tmp/heap.pprof
```

---

## 2. Mac Baseline Matrix Results

### 2.1 Build — AOI Index Insertion

| Scale | Density | MoverCount | Inserts/s (min/median/max) | Build Time (min/median/max) | Peak RSS (min/median/max) | Heap Inuse |
|---|---|---|---|---|---|---|
| 100k | normal | 10k | 35,761 / 37,340 / 37,695 | 2.65s / 2.68s / 2.80s | 510MB / 512MB / 512MB | ~376MB |
| 1M | normal | 10k | 19,590 / 21,370 / 21,747 | 45.98s / 46.79s / 51.05s | 3.18GB / 3.26GB / 3.35GB | ~3.42GB |

**Key finding:** 1M-player insertion throughput drops to ~57% of 100k throughput. Build time is roughly linear with scale (1M takes ~17.5x 100k at 10x the player count). Peak RSS at 1M reaches 3.35GB.

### 2.2 Core Tick — AOI Index-Only Steady State

| Scale | Density | MoverCount | Tick Median (min/median/max) | Tick P95 | Tick P99 | Moves/s (min/median/max) | Peak RSS |
|---|---|---|---|---|---|---|---|
| 100k | normal | 10k | 272.8ms / 273.7ms / 303.5ms | 279.0ms | 304.8ms | 32,419 / 36,576 / 36,729 | ~588MB |

**AOI Diagnostic Counters (per 100-tick run, 10k movers):**
- Candidate pairs: 150.8M per run (1.51M per tick)
- Distance checks: 202.3M per run (2.02M per tick)
- Relationships entered: 36,023
- Relationships left: 37
- Visibility churn per mover: mean 5.74, p50 6, p95 9, max 14

**Key finding:** Core tick median latency is ~274ms, 2.7x the 100ms budget. The p99 reaches 305ms. Each tick processes ~1.51M candidate pairs from nine-cell traversal.

### 2.3 World + AOI — Full Simulation

| Scale | Density | MoverCount | Combined Median (min/median/max) | Sim Median | AOI Prep Median | Budget Exceeded | Peak RSS |
|---|---|---|---|---|---|---|---|
| 100k | normal | 10k | 306.9ms / 310.6ms / 310.7ms | 2.82ms | 301.5ms | 100/100 | ~688MB |

**Key finding:** AOI preparation dominates combined tick time at 98% (301.5ms of 306.9ms). World simulation is negligible at 2.8ms. All 100 measured AOI updates exceed the 100ms budget.

---

## 3. Mac Limitations — 1M Incomplete

The following baseline scenarios **could not complete** on the Mac M5 16GB:

| Scenario | Status | Reason |
|---|---|---|
| core_tick / 1M / 10k / normal | Not run | 16GB RAM — workload generation + AOI index exceed available memory |
| world_aoi / 1M / 10k / normal | Not run | 16GB RAM — World + AOI index simultaneous residency too large |
| build / 1M / 50k / hotspot | Interrupted | Memory guard exceeded during workload generation |
| All 1M / 50k variants | Not run | 5x larger movement schedule + hotspot relationship explosion |
| All 500k scenarios | Not run | Deferred to Full Matrix on Linux |

**Root cause:** The workload generator creates temporary auxiliary AOI indices during density measurement and visibility churn measurement, effectively doubling peak memory. At 1M scale, the combined resident set exceeds 12GB (75% of 16GB), triggering the memory guard. The 1M/10k build-only mode succeeds (3.5GB RSS) but leaves insufficient headroom for the additional structures needed by core_tick (expanded schedule buffers) and world_aoi (full World state + player objects).

---

## 4. Profile Analysis — 100k/10k/normal (Post-A1 Optimization)

Profile data reflects the A1 allocation-optimized codebase (commit `d174e9a`).

### 4.1 Core Tick CPU Profile

Top consumers (47.51s sampling duration, 47.61s total samples):

| Function | Flat% | Cum% | Category |
|---|---|---|---|
| `runtime.mapaccess2_faststr` | 9.68% | 27.05% | Map lookup (2-key) |
| `runtime.(*Iter).Next` | 8.88% | 10.65% | Map iteration |
| `runtime.mapaccess1_faststr` | 5.99% | 13.57% | Map lookup (1-key) |
| `runtime.memequal` | 4.68% | 4.68% | String comparison |
| `runtime.mapassign_faststr` | 2.44% | 5.31% | Map insert |
| `game.(*AOIIndex).IsVisible` (inline) | 2.71% | **28.42%** | Visibility check |
| `game.(*AOIIndex).recalculateRelationships` | 1.53% | **54.42%** | AOI recalculation |
| `runtime.memclrNoHeapPointers` | 1.28% | 1.28% | GC zeroing |

**Interpretation:** After A1 allocation optimization, GC zeroing collapsed from 24.24% to 1.28%. Map operations are now the dominant category (~29% combined: `mapaccess2` + `mapaccess1` + `Iter.Next` + `mapassign`). `IsVisible` cumulatively accounts for 28.42% of CPU — it's a simple two-key map lookup, but called millions of times per tick. `recalculateRelationships` cumulatively drives 54.42% of all CPU — this single function is the bottleneck.

### 4.2 World + AOI CPU Profile

Top consumers (48.80s sampling duration, 48.07s total samples):

| Function | Flat% | Cum% | Category |
|---|---|---|---|
| `runtime.mapaccess2_faststr` | 9.53% | 26.84% | Map lookup (2-key) |
| `runtime.(*Iter).Next` | 8.38% | 10.42% | Map iteration |
| `runtime.mapaccess1_faststr` | 5.64% | 13.36% | Map lookup (1-key) |
| `runtime.memequal` | 4.89% | 4.89% | String comparison |
| `runtime.mapassign_faststr` | 2.12% | 4.16% | Map insert |
| `game.(*AOIIndex).IsVisible` (inline) | 2.10% | **26.77%** | Visibility check |
| `game.(*AOIIndex).recalculateRelationships` | 1.54% | **50.84%** | AOI recalculation |
| `runtime.memclrNoHeapPointers` | 1.37% | 1.37% | GC zeroing |

**Interpretation:** World + AOI profile mirrors core_tick nearly identically. `recalculateRelationships` cum drives half the CPU, `IsVisible` cum drives another quarter. World simulation (`World.Step`) cumulatively accounts for only 12% — confirming the bottleneck remains AOI, not movement physics.

### 4.3 Heap Profile

Both modes show ~3.7-4.8MB in-use at profile capture time, dominated by profiling infrastructure (`pprof.StartCPUProfile`, `compress/gzip`). Post-A1, workload heap is ~107-164MB (down from ~150-240MB), reflecting reduced allocation churn during measurement. The heap delta per run dropped from ~37GB to ~14GB.

---

## 5. Optimization Candidates

Evidence-backed findings from the baseline. Items marked ✓ were addressed by the A1 allocation optimization (Section 9).

### 5.1 Map Overhead Dominance (remaining)
~29% of CPU time spent on map operations (`mapaccess2` + `mapaccess1` + `Iter.Next` + `mapassign`). The three-layer `map[string]map[string]struct{}` structure for `visible` relationships means every lookup traverses multiple hash tables. `IsVisible` alone has 28.42% cumulative CPU, called millions of times per tick. Converting string IDs to integer indices and using arrays or dense maps would reduce this overhead significantly.

### 5.2 ✓ Relationship Allocation (addressed by A1)
~~Each `sortedCopy` call in `recalculateRelationships` allocates a new slice and sorts it.~~ Removed in A1: `nineCellCandidates` no longer allocates a temporary map or sorts keys; `sortedCopy` replaced with direct tracking via reusable buffers.

### 5.3 ✓ GC Zeroing (addressed by A1)
~~`memclrNoHeapPointers` at 24-25% indicates large volumes of freshly allocated memory being zeroed.~~ Collapsed to 1.28% in A1 via pre-allocated reusable buffers and elimination of per-update map/slice allocations.

### 5.4 Nine-Cell Candidate Explosion (remaining)
Each tick examines ~1.51M candidate pairs for 10k movers. The `IsVisible` fast-path filter eliminates most candidates before distance checks, but the nine-cell linear scan is still O(movers × cell_population). An incremental approach that only rechecks candidates near cell boundaries could reduce this volume.

### 5.5 Relationship Churn is Low (remaining)
Mean churn per mover is 5.74 entered+left relationships per update, out of ~240 visible neighbors. Only ~2.4% of relationships change per tick, meaning ~97.6% of `recalculateRelationships` work re-establishes existing relationships. Incremental cell-boundary recheck would exploit this directly.

---

## 6. Data Completeness

| | 100k/10k/normal | 1M/10k/normal | 1M/50k/sparse | 1M/50k/normal | 1M/50k/hotspot |
|---|---|---|---|---|---|
| Build | 3/3 repeats | 3/3 repeats | — | — | 0/3 (OOM) |
| Core Tick | 3/3 repeats | — | — | — | — |
| World+AOI | 3/3 repeats | — | — | — | — |
| Profiles | CPU + Heap | — | — | — | — |

**—** = Mac 16GB insufficient, deferred to Linux.

---

## 7. Reserved: Linux Full Matrix

The **Full Matrix** (500k scale, all three densities, all 18 configs × 3 modes) and the remaining **1M Baseline Matrix** entries (core_tick, world_aoi, 50k-mover variants) are reserved for a Linux target with:

- **CPU:** 32 vCPU minimum, recommended 64 vCPU
- **RAM:** 64 GB
- **Go:** 1.26+

This will produce a complete scaling curve from 100k → 500k → 1M across all densities, enabling regression benchmarking of future AOI optimizations against this Mac baseline.

---

## 8. Summary

| Metric | 100k/10k/normal |
|---|---|
| Build throughput | 37k inserts/s |
| Core tick median | 274ms |
| World+AOI combined median | 307ms |
| AOI prep % of combined | 98% |
| Budget compliance (100ms) | 0/100 ticks |
| Peak RSS (world_aoi) | 688MB |
| Per-tick candidate pairs | 1.51M |
| Per-tick distance checks | 2.02M |
| Visibility churn rate | ~2.4% per tick |

**Primary bottleneck:** AOI relationship recalculation in `recalculateRelationships`, driven by Go map overhead and nine-cell linear scan. World simulation cost is negligible at 100k scale.

---

## 9. A1 Comparison — AOI Allocation Optimization

**Optimization:** Task 1 (ordering contracts) + Task 2 (remove movement-path candidate collection and sorting).  
**Optimized commit:** `d174e9a7bff79b8a843bcf5a6106016e2289fa22` (includes `4a596ad` ordering contracts)  
**Baseline commit:** `3af14009eb6d104496da69176fdf26a252c63e67`  
**Measured:** 2026-06-14  
**Environment:** Apple M5, 10 cores, 16GB RAM, macOS darwin/arm64, Go 1.26.3, seed 42

### 9.1 Reproduction

```sh
go build -o /tmp/aoi-bench ./cmd/aoi-bench

/tmp/aoi-bench -mode core_tick -scale 100000 -movers 10000 -density normal -seed 42 -repeat 1 \
  > docs/benchmarks/profiles/100k-10k-normal-core-a1-repeat1.json
/tmp/aoi-bench -mode core_tick -scale 100000 -movers 10000 -density normal -seed 42 -repeat 2 \
  > docs/benchmarks/profiles/100k-10k-normal-core-a1-repeat2.json
/tmp/aoi-bench -mode core_tick -scale 100000 -movers 10000 -density normal -seed 42 -repeat 3 \
  > docs/benchmarks/profiles/100k-10k-normal-core-a1-repeat3.json
```

Result artifacts: `docs/benchmarks/profiles/100k-10k-normal-core-a1-repeat{1,2,3}.json`

### 9.2 Core Tick — Before vs After (100k / 10k / normal)

| Metric | Baseline (3af1400) min / median / max | Optimized (d174e9a) min / median / max | Change (median) |
|---|---|---|---|
| Tick median | 272.8ms / 273.7ms / 303.5ms | 101.1ms / 102.0ms / 103.0ms | **−62.7%** |
| Tick P95 | 277.8ms / 279.0ms / 337.0ms | 102.9ms / 103.8ms / 104.7ms | **−62.8%** |
| Moves/s | 32,419 / 36,576 / 36,729 | 96,488 / 98,174 / 98,459 | **+168%** |
| Δ total alloc (per run) | ~36.9 GB / ~36.9 GB / ~36.9 GB | ~14.4 GB / ~14.4 GB / ~14.4 GB | **−61%** |
| Δ GC cycles (per run) | 308 / 310 / 310 | 227 / 227 / 229 | **−27%** |
| Δ GC pause (per run) | ~11.8ms / ~11.8ms / ~11.8ms | ~8.6ms / ~8.8ms / ~8.5ms | **−27%** |
| Peak RSS | 612MB / 612MB / 616MB | 483MB / 491MB / 492MB | **−20%** |

### 9.3 Semantic Equivalence Diagnostics

| Counter | Baseline | Optimized (all 3 repeats) | Match |
|---|---|---|---|
| Candidate pairs | 150,832,757 | 150,832,757 | ✓ |
| Distance checks | 202,294,335 | 202,294,335 | ✓ |
| Relationships entered | 36,023 | 36,023 | ✓ |
| Relationships left | 37 | 37 | ✓ |
| Visibility churn (mean) | 5.74 | 5.74 | ✓ |

All three optimized repeats completed successfully with identical AOI diagnostic totals, confirming no visibility behavior change.

### 9.4 Conclusion

The optimization delivers both **allocation reduction and latency improvement**:

- Removing per-update `nineCellCandidates` (`seen` map + sorted key extraction), `sortedCopy`, and sorted `setKeys` traversal eliminated ~61% of measured-tick heap allocation and ~27% of GC cycles.
- Core tick median dropped from **274ms to 102ms** (2.7× throughput), with P95 falling from **279ms to 104ms**. The 100ms AOI preparation budget is now nearly met (median +4%, P95 +4%).
- Peak RSS declined ~20%, likely from lower allocation churn rather than structural memory savings.

This A1 change addresses items 5.2 and 5.3 from Section 5. Remaining candidates (integer ID indexing, incremental boundary recheck, nine-cell population scaling) are unchanged and deferred.
