# AOI Allocation And Sorting Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Track each
> task with its checkbox.

**Goal:** Reduce temporary allocation and unnecessary sorting in the AOI
movement hot path without changing visibility behavior.

**Architecture:** AOI collections become explicitly unordered, while realtime
message encoding remains responsible for deterministic wire ordering. The
existing benchmark scenario is rerun after the focused hot-path change.

**Tech Stack:** Go 1.26, existing AOI benchmark runner, Go tests, Go vet.

**Required context:** Read
`docs/superpowers/specs/2026-06-14-aoi-allocation-optimization-design.md`,
`docs/benchmarks/aoi-core-baseline.md`, and `docs/map-walker-handoff.md`.

---

### Task 1: Freeze AOI And Protocol Ordering Contracts

- [x] Establish unordered AOI collection semantics while preserving stable
  realtime snapshot output.

**Task boundary:**

- Change AOI tests so multi-element `RelationshipChanges.Entered`,
  `RelationshipChanges.Left`, and `VisibleNeighbors` results are compared as
  sets rather than ordered slices.
- Preserve order-sensitive coverage for the `Remove` cold path.
- Add multi-Cell coverage proving candidates in different neighboring Cells
  are neither omitted nor duplicated.
- Make visible-entity snapshot encoding sort a copied player-state slice by
  player ID.
- Verify snapshot encoding does not mutate its input.
- Do not change replication-update normalization or sorting.

**Behavioral goals:**

- AOI collection order is no longer part of the public behavior contract.
- Entry, exit, hysteresis, removal, and symmetric visibility semantics remain
  unchanged.
- Initial snapshot JSON remains deterministic by player ID.
- Message encoding owns ordering required for stable wire output.

**Affected modules:**

- Modify `internal/game/aoi_test.go`
- Modify `internal/realtime/messages.go`
- Modify `internal/realtime/messages_test.go`

**Verification:**

- Run `go test ./internal/game ./internal/realtime`.
- Confirm unordered AOI tests pass across repeated runs.
- Confirm an unsorted snapshot input encodes in player-ID order.
- Confirm the snapshot input slice retains its original order after encoding.

---

### Task 2: Remove AOI Movement-Path Candidate And Sorting Allocations

- [x] Replace temporary candidate collection and repeated sorting with direct
  map traversal.

**Task boundary:**

- Make `VisibleNeighbors` build an unsorted result slice directly from the
  visible-neighbor map.
- Make `recalculateRelationships` traverse the nine neighboring Cell maps
  directly.
- Remove the movement-path `seen` map, candidate slice extraction, candidate
  sorting, and second candidate traversal.
- Return `Entered` and `Left` in discovery order without sorting.
- Detect leaving relationships in one pass over the current visible set, then
  remove those symmetric relationships in a second pass over `Left`.
- Remove helpers that become unused only because of this hot-path change.
- Keep the shared sorted `game.setKeys` helper unchanged.
- Keep `AOIIndex.Remove`, Cell geometry, enter/leave radii, string IDs,
  symmetric adjacency, and the nine-Cell search area unchanged.

**Behavioral goals:**

- Each nearby candidate is checked at most once per moved player.
- `RelationshipChanges` still contains every entered and left player exactly
  once.
- No relationship map is mutated through `removeRelationship` while that map
  is being ranged.
- Candidate-pair, distance-check, entered, and left totals remain identical for
  the frozen workload.
- The change reduces temporary allocation without introducing pooling, scratch
  buffers, batch APIs, or a new AOI algorithm.

**Affected modules:**

- Modify `internal/game/aoi.go`
- Modify `internal/game/aoi_test.go` only where additional direct-traversal
  coverage is required

**Verification:**

- Run `go test ./internal/game`.
- Run `go test ./internal/realtime`.
- Run `go test ./internal/benchmark/...`.
- Run `go test ./...`.
- Run `go vet ./...`.
- Confirm AOI tests still cover entry, exit, hysteresis, symmetry, removal, and
  multi-Cell candidates.

---

### Task 3: Measure And Document The A1 Comparison

- [x] Re-run the frozen Mac Core scenario and append an evidence-based
  comparison to the baseline report.

**Task boundary:**

- Build the benchmark command from the optimized commit.
- Run Core Tick for 100,000 Observers, 10,000 movers, normal density, seed 42,
  with three process-isolated repeats.
- Compare optimized results with the existing baseline rather than replacing
  the original measurements.
- Record Core Tick median and P95, moves per second, allocation bytes,
  allocation count, GC behavior, and peak RSS.
- Record candidate pairs, distance checks, relationships entered, and
  relationships left as semantic-equivalence diagnostics.
- Append an A1 comparison section to the existing report.
- Do not run the Linux Full Matrix, one-million-player scenarios, World-plus-AOI
  scenarios, or further AOI optimizations.

**Behavioral goals:**

- Baseline and optimized results remain visibly distinct and reproducible.
- Allocation bytes or allocation count clearly decline.
- Median and P95 do not materially regress.
- Diagnostic AOI totals remain identical.
- The report claims a latency or throughput improvement only when the measured
  results support it.
- Allocation reduction without latency improvement is documented as a valid
  result rather than reframed as a speedup.

**Affected modules:**

- Modify `docs/benchmarks/aoi-core-baseline.md`
- Add optimized benchmark result artifacts under the existing
  `docs/benchmarks/` result structure if required by the runner
- Update `docs/map-walker-handoff.md` after the comparison and project
  verification are complete

**Verification:**

- Run `go build -o /tmp/aoi-bench ./cmd/aoi-bench`.
- Run `/tmp/aoi-bench -mode core_tick -scale 100000 -movers 10000 -density normal -seed 42 -repeat 1`.
- Run `/tmp/aoi-bench -mode core_tick -scale 100000 -movers 10000 -density normal -seed 42 -repeat 2`.
- Run `/tmp/aoi-bench -mode core_tick -scale 100000 -movers 10000 -density normal -seed 42 -repeat 3`.
- Confirm all three repeats complete and are represented in the comparison.
- Confirm candidate pairs, distance checks, relationships entered, and
  relationships left match the original frozen workload.
- Confirm the report includes the exact command, optimized commit, environment,
  before/after metrics, and conclusion.
- Run `go test ./...`.
- Run `go vet ./...`.
