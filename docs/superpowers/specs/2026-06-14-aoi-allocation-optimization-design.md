# AOI Allocation And Sorting Optimization Design

Date: 2026-06-14

## Goal

Reduce temporary allocation and unnecessary sorting in the current AOI
movement hot path, then measure the result against the existing
100,000-Observer / 10,000-mover / normal-density baseline.

This is the first profile-driven optimization pass. It does not need to bring
the AOI Tick below 100ms; it must establish a trustworthy profile-to-change-to-
benchmark engineering loop.

## Evidence

The Mac baseline in `docs/benchmarks/aoi-core-baseline.md` shows:

- Core AOI Tick median is approximately 274ms.
- World-plus-AOI preparation median is approximately 301ms while World
  simulation is approximately 2.8ms.
- Map operations and GC-related work dominate the CPU profile.
- `nineCellCandidates` constructs, extracts, and sorts a temporary candidate
  set for every mover.
- Existing-neighbor checks copy and sort visible IDs before scanning them.
- Relationship changes are sorted in AOI and normalized again at the realtime
  message boundary.

## Scope

This phase includes:

- Removing temporary candidate-set construction from AOI movement.
- Removing movement-path sorting that has no game-semantic purpose.
- Preserving deterministic ordering at realtime protocol boundaries.
- Updating tests to distinguish unordered AOI results from ordered protocol
  output.
- Re-running the existing Mac Core benchmark scenario and documenting the
  comparison.

This phase excludes:

- Incremental AOI algorithms.
- Cell-size or search-area changes.
- Integer player IDs or adjacency representation changes.
- Object pools, scratch-buffer ownership, or batch APIs.
- Hub, WebSocket, encoding, or gameplay performance optimization.
- Linux Full Matrix or one-million-player reruns.

## Behavioral Contract

The following behavior must remain unchanged:

- Each player belongs to exactly one Cell.
- AOI entry uses the 500m enter radius.
- AOI exit uses the 600m leave radius.
- Visibility relationships remain symmetric.
- The nine neighboring Cells remain the candidate search area.
- `RelationshipChanges` contains every entered and left player exactly once.
- Initial snapshots and replication messages contain the same logical data.

AOI collection order is not game behavior:

- `RelationshipChanges.Entered` and `RelationshipChanges.Left` are unordered.
- `AOIIndex.VisibleNeighbors` is unordered.
- Realtime message encoding owns deterministic wire ordering.

## AOI Hot Path

### Candidate Traversal

`recalculateRelationships` directly traverses the members of each of the nine
Cells.

It does not:

- Build a temporary `seen` map.
- Extract candidate map keys into a slice.
- Sort candidate IDs.
- Traverse a second candidate collection.

This is safe because a player belongs to exactly one Cell, so members of the
nine distinct Cells cannot be duplicated.

Candidate traversal still:

- Skips the moving player.
- Skips players already visible to the mover.
- Performs the same exact-distance check.
- Establishes the same symmetric relationship when inside the enter radius.

### Leaving Relationships

Leaving relationships use two phases:

1. Traverse the mover's visible-neighbor map and append neighbors beyond the
   leave radius to `Left`.
2. Traverse `Left` and remove each symmetric relationship.

The implementation does not mutate the ranged visible map through
`removeRelationship`. The `Left` slice is already required as the method's
result, so no separate deletion buffer is introduced.

### Sorting

`recalculateRelationships` returns discovered `Entered` and `Left` IDs without
sorting.

`VisibleNeighbors` constructs and returns an unsorted slice directly from the
visible-neighbor map.

The shared `game.setKeys` helper remains sorted because World and cold paths
may rely on its current deterministic behavior.

`AOIIndex.Remove` remains unchanged and sorted. Disconnect is not part of the
10Hz movement hot path.

## Protocol Boundary

`EncodeVisibleEntitiesSnapshot` sorts a copy of its player states by player ID
before JSON encoding.

It must not mutate the caller's input slice.

`replication_update` retains its existing normalization and deterministic
sorting behavior. AOI movement results therefore remain unordered internally
while wire output remains stable for tests, logs, and diagnosis.

## Verification

### Functional Tests

- Convert multi-element AOI order assertions to set-equivalence assertions.
- Preserve tests for entry, exit, hysteresis, removal, and symmetric
  visibility.
- Add a multi-Cell candidate test proving direct traversal neither omits a
  candidate nor establishes a duplicate relationship.
- Add a visible-neighbor test that treats the result as unordered.
- Add a snapshot encoding test with unsorted input and assert player-ID-ordered
  JSON output.
- Assert snapshot encoding does not mutate its input slice.
- Run `go test ./...`.
- Run `go vet ./...`.

### Performance Comparison

Use the existing frozen scenario:

- 100,000 Observers.
- 10,000 movers.
- Normal density.
- Seed 42.
- Three process-isolated repeats.

Compare the optimized result with the baseline report:

- Core Tick median and P95.
- Moves per second.
- Allocation bytes and allocation count.
- GC behavior.
- Peak RSS.

The following diagnostic totals must remain unchanged for the same workload:

- Candidate pairs.
- Distance checks.
- Relationships entered.
- Relationships left.

## Success Criteria

The phase succeeds when:

- All functional tests and vet pass.
- Allocation bytes or allocation count per measured workload clearly decline.
- Core Tick median and P95 do not materially regress.
- AOI diagnostic totals remain identical.
- The comparison is appended to
  `docs/benchmarks/aoi-core-baseline.md`.

If allocation declines without a latency improvement, the result is still
valid and documented without claiming a throughput win.

## Follow-up

After this phase, the project moves to a small server-authoritative gameplay
vertical slice unless the comparison reveals a correctness regression or a
specific reason to revise this optimization.

Incremental AOI maintenance remains a separate future design. Real WebSocket
load testing follows the gameplay slice so the measured message and fan-out
workload represents actual game behavior.
