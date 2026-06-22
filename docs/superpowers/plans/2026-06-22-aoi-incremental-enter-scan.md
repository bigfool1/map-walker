# AOI Incremental Enter Scan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce AOI movement cost by skipping nine-cell enter-candidate scans
for small same-cell moves, while preserving exact leave detection and bounded
enter-discovery delay.

**Architecture:** Split `MoveDetailed` into three steps — position/cell update,
exact leave check against existing visible neighbors, and a conditional
nine-cell enter scan. Enter scans run only when the player changed cell, has no
prior enter-scan marker, or moved at least `EnterRescanDistanceMeters` since
its last enter scan. `Insert` and `RecalculateRelationships` always do a full
scan. Visibility symmetry, 500m enter radius, 600m leave radius, and cell
geometry are unchanged.

**Tech Stack:** Go 1.26, `internal/game/aoi.go`, `internal/game/aoi_test.go`,
existing `MovementDelta` and `AOIStats`, existing `HubSnapshot` and stats log,
existing `BenchmarkHubReplication`, new `BenchmarkAOIMove` under
`internal/game/`.

**Required context:** Read `AGENTS.md`,
`docs/superpowers/specs/2026-06-21-aoi-incremental-enter-scan-design.md`,
`internal/game/aoi.go`, `internal/realtime/stats.go`, and
`internal/realtime/hub.go` lines 864–1063 before implementation.

---

## Global Constraints

- Do not change `EnterRadiusMeters` (500m) or `LeaveRadiusMeters` (600m).
- Do not change `CellSizeMeters` (600m) or cell membership rules.
- Do not change relationship symmetry: every entered/left mutation must update
  both directions in `a.visible`.
- Leave detection remains exact: existing visible neighbors are always checked
  against the 600m leave radius on every `MoveDetailed`.
- Do not change `replication_update` protocol fields.
- Do not change `MovementDelta` shape (`PlayerID`, `Entered`, `Left`,
  `Stable`).
- `Insert` and `RecalculateRelationships` always run a full nine-cell enter
  scan.
- Tasks 1–5 share the AOI hot path. Use a single agent for those tasks in
  order. Do not run another agent against `internal/game/aoi.go` or
  `internal/realtime/hub.go:864-1063` concurrently.

---

### Task 1: Add Config, Player Marker, And Stats Fields

- [ ] Add data plumbing for incremental enter scan without changing behavior
  yet.

**Files:**

- Modify: `internal/game/aoi.go`
- Modify: `internal/game/aoi_test.go`

**Interfaces:**

- Produces:
  - `AOIConfig.EnterRescanDistanceMeters float64`
  - `AOIConfigFromWorld(...)` returns config with
    `EnterRescanDistanceMeters: 50`.
  - `aoiPlayer.lastEnterScanX float64`, `lastEnterScanY float64`,
    `lastEnterScanCell CellCoord`, `hasEnterScanMarker bool`.
  - `AOIStats.FullEnterScans uint64`, `SkippedEnterScans uint64`,
    `LeaveChecks uint64`, `StableRelationships uint64`.
  - Helper `(c AOIConfig) enterRescanDistanceSquared() float64`.

- [ ] **Step 1: Write failing test for default rescan distance**

Add to `internal/game/aoi_test.go`:

```go
func TestAOIConfigDefaultEnterRescanDistance(t *testing.T) {
	cfg := AOIConfigFromWorld(testConfig())
	if cfg.EnterRescanDistanceMeters != 50 {
		t.Fatalf("EnterRescanDistanceMeters = %v, want 50", cfg.EnterRescanDistanceMeters)
	}
	if cfg.enterRescanDistanceSquared() != 2500 {
		t.Fatalf("enterRescanDistanceSquared = %v, want 2500", cfg.enterRescanDistanceSquared())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/game -run TestAOIConfigDefaultEnterRescanDistance -v`
Expected: FAIL with "EnterRescanDistanceMeters undefined" or similar compile
error.

- [ ] **Step 3: Add config field, helper, and default**

In `internal/game/aoi.go`:

```go
type AOIConfig struct {
	OriginLat                  float64
	OriginLng                  float64
	CellSizeMeters             float64
	EnterRadiusMeters          float64
	LeaveRadiusMeters          float64
	EnterRescanDistanceMeters  float64
}

func AOIConfigFromWorld(config Config) AOIConfig {
	return AOIConfig{
		OriginLat:                 config.SpawnLat,
		OriginLng:                 config.SpawnLng,
		CellSizeMeters:            defaultCellSizeMeters,
		EnterRadiusMeters:         defaultEnterRadiusMeters,
		LeaveRadiusMeters:         defaultLeaveRadiusMeters,
		EnterRescanDistanceMeters: defaultEnterRescanDistanceMeters,
	}
}

func (c AOIConfig) enterRescanDistanceSquared() float64 {
	return c.EnterRescanDistanceMeters * c.EnterRescanDistanceMeters
}
```

Add to the existing `const (...)` block:

```go
const (
	defaultCellSizeMeters             = 600
	defaultEnterRadiusMeters          = 500
	defaultLeaveRadiusMeters          = 600
	defaultEnterRescanDistanceMeters  = 50
)
```

- [ ] **Step 4: Add aoiPlayer marker fields**

In `internal/game/aoi.go`:

```go
type aoiPlayer struct {
	lat, lng          float64
	localX, localY    float64
	cell              CellCoord
	lastEnterScanX    float64
	lastEnterScanY    float64
	lastEnterScanCell CellCoord
	hasEnterScanMarker bool
}
```

No other code reads these yet.

- [ ] **Step 5: Extend AOIStats with new counters**

In `internal/game/aoi.go`:

```go
type AOIStats struct {
	CandidatePairs       uint64
	DistanceChecks       uint64
	RelationshipsEntered uint64
	RelationshipsLeft    uint64
	FullEnterScans       uint64
	SkippedEnterScans    uint64
	LeaveChecks          uint64
	StableRelationships  uint64
}
```

- [ ] **Step 6: Run all AOI tests to confirm no behavior regression**

Run: `go test ./internal/game -v`
Expected: PASS. The new test passes; all existing tests still pass because no
movement code reads the new fields yet.

- [ ] **Step 7: Commit**

```bash
git add internal/game/aoi.go internal/game/aoi_test.go
git commit -m "feat(aoi): add EnterRescanDistanceMeters config and marker fields"
```

---

### Task 2: Extract Leave-Only Check From recalculateRelationships

- [ ] Refactor `recalculateRelationships` into two separately callable helpers
  with no behavior change.

**Files:**

- Modify: `internal/game/aoi.go`
- Modify: `internal/game/aoi_test.go`

**Interfaces:**

- Produces:
  - `(a *AOIIndex) enterScanNineCells(self *aoiPlayer, playerID int64) []int64`
    — returns newly entered neighbor IDs, mutates relationships and
    `CandidatePairs`/`DistanceChecks`/`RelationshipsEntered` stats. Caller is
    responsible for incrementing `FullEnterScans` (Task 3 will do this).
  - `(a *AOIIndex) leaveCheckExistingNeighbors(self *aoiPlayer, playerID int64) []int64`
    — returns left neighbor IDs, mutates relationships and `DistanceChecks` and
    `RelationshipsLeft` and `LeaveChecks` stats.
  - `recalculateRelationships` still exists and now composes the two helpers
    plus increments `FullEnterScans`.

- [ ] **Step 1: Write a refactor-safety test (snapshot of stats counts)**

Add to `internal/game/aoi_test.go`:

```go
func TestAOIRecalculateRelationshipsStatsAccounting(t *testing.T) {
	aoi := newTestAOI()
	const a, b, c int64 = 5001, 5002, 5003
	aLat, aLng := localLatLng(aoi.config, 0, 0)
	bLat, bLng := localLatLng(aoi.config, 300, 0)
	cLat, cLng := localLatLng(aoi.config, 800, 0)
	aoi.Insert(a, aLat, aLng)
	aoi.Insert(b, bLat, bLng)
	aoi.Insert(c, cLat, cLng)
	aoi.TakeStats()

	aoi.RecalculateRelationships(a)
	stats := aoi.TakeStats()
	if stats.FullEnterScans != 1 {
		t.Fatalf("FullEnterScans = %d, want 1", stats.FullEnterScans)
	}
	if stats.LeaveChecks == 0 {
		t.Fatal("LeaveChecks should be > 0 when there are existing visible neighbors")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/game -run TestAOIRecalculateRelationshipsStatsAccounting -v`
Expected: FAIL because `FullEnterScans` and `LeaveChecks` are never
incremented.

- [ ] **Step 3: Extract `enterScanNineCells`**

In `internal/game/aoi.go`, replace the body of `recalculateRelationships` step
by step. First add the enter helper, leaving the leave logic inline:

```go
func (a *AOIIndex) enterScanNineCells(self *aoiPlayer, playerID int64) []int64 {
	entered := make([]int64, 0)
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for candidateID := range a.cells[CellCoord{X: self.cell.X + dx, Y: self.cell.Y + dy}] {
				if candidateID == playerID || a.IsVisible(playerID, candidateID) {
					continue
				}
				a.stats.CandidatePairs++
				candidate := a.players[candidateID]
				a.stats.DistanceChecks++
				if a.withinEnterRadius(self, candidate) {
					if a.addRelationship(playerID, candidateID) {
						entered = append(entered, candidateID)
						a.stats.RelationshipsEntered++
					}
				}
			}
		}
	}
	return entered
}
```

- [ ] **Step 4: Extract `leaveCheckExistingNeighbors`**

```go
func (a *AOIIndex) leaveCheckExistingNeighbors(self *aoiPlayer, playerID int64) []int64 {
	left := make([]int64, 0)
	for neighborID := range a.visible[playerID] {
		neighbor := a.players[neighborID]
		if neighbor == nil {
			continue
		}
		a.stats.DistanceChecks++
		a.stats.LeaveChecks++
		if a.beyondLeaveRadius(self, neighbor) {
			left = append(left, neighborID)
		}
	}
	for _, neighborID := range left {
		if a.removeRelationship(playerID, neighborID) {
			a.stats.RelationshipsLeft++
		}
	}
	return left
}
```

- [ ] **Step 5: Rewrite `recalculateRelationships` to compose them**

```go
func (a *AOIIndex) recalculateRelationships(playerID int64) RelationshipChanges {
	self, exists := a.players[playerID]
	if !exists {
		return RelationshipChanges{}
	}
	entered := a.enterScanNineCells(self, playerID)
	a.stats.FullEnterScans++
	left := a.leaveCheckExistingNeighbors(self, playerID)
	return RelationshipChanges{Entered: entered, Left: left}
}
```

- [ ] **Step 6: Run all AOI tests**

Run: `go test ./internal/game -v`
Expected: PASS. The new test now passes; existing tests still pass because the
combined behavior is identical.

- [ ] **Step 7: Commit**

```bash
git add internal/game/aoi.go internal/game/aoi_test.go
git commit -m "refactor(aoi): split recalculateRelationships into enter scan and leave check"
```

---

### Task 3: Implement Bounded-Delay Enter Scan In MoveDetailed

- [ ] Make `MoveDetailed` skip the nine-cell enter scan when the move is small
  and same-cell, while always running the leave check.

**Files:**

- Modify: `internal/game/aoi.go`
- Modify: `internal/game/aoi_test.go`

**Interfaces:**

- Produces:
  - `MoveDetailed` may return `MovementDelta{Entered: nil}` even when a
    not-yet-visible neighbor is within 500m.
  - `Insert` sets the enter-scan marker on first relationship establishment.
  - `RecalculateRelationships` refreshes the enter-scan marker.
  - `AOIStats.SkippedEnterScans` and `FullEnterScans` reflect the choice for
    each `MoveDetailed` call.

- [ ] **Step 1: Write failing tests for skip and force conditions**

Add to `internal/game/aoi_test.go`:

```go
func TestAOIMoveDetailedSkipsEnterScanForSmallSameCellMove(t *testing.T) {
	aoi := newTestAOI()
	const mover, neighbor int64 = 6001, 6002
	moverLat, moverLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(mover, moverLat, moverLng)
	// Neighbor at 450m: Insert pulls them into mutual visibility (< 500m).
	// Remove the relationship directly so we can prove that a small move
	// does NOT rediscover the neighbor through a skipped scan.
	neighborLat, neighborLng := localLatLng(aoi.config, 450, 0)
	aoi.Insert(neighbor, neighborLat, neighborLng)
	aoi.removeRelationship(mover, neighbor)
	aoi.TakeStats()

	smallLat, smallLng := localLatLng(aoi.config, 10, 0)
	delta := aoi.MoveDetailed(mover, smallLat, smallLng)

	if len(delta.Entered) != 0 {
		t.Fatalf("Entered = %v, want empty (small same-cell move should skip enter scan)", delta.Entered)
	}
	stats := aoi.TakeStats()
	if stats.SkippedEnterScans != 1 {
		t.Fatalf("SkippedEnterScans = %d, want 1", stats.SkippedEnterScans)
	}
	if stats.FullEnterScans != 0 {
		t.Fatalf("FullEnterScans = %d, want 0", stats.FullEnterScans)
	}
}

func TestAOIMoveDetailedForcesEnterScanBeyondThreshold(t *testing.T) {
	aoi := newTestAOI()
	const mover, neighbor int64 = 6101, 6102
	moverLat, moverLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(mover, moverLat, moverLng)
	neighborLat, neighborLng := localLatLng(aoi.config, 450, 0)
	aoi.Insert(neighbor, neighborLat, neighborLng)
	aoi.removeRelationship(mover, neighbor)
	aoi.TakeStats()

	// Move 60m (above default 50m threshold) within the same cell.
	farLat, farLng := localLatLng(aoi.config, 60, 0)
	delta := aoi.MoveDetailed(mover, farLat, farLng)

	if !sliceContains(delta.Entered, neighbor) {
		t.Fatalf("Entered = %v, want to contain %d after crossing threshold", delta.Entered, neighbor)
	}
	stats := aoi.TakeStats()
	if stats.FullEnterScans != 1 {
		t.Fatalf("FullEnterScans = %d, want 1", stats.FullEnterScans)
	}
}

func TestAOIMoveDetailedForcesEnterScanOnCellChange(t *testing.T) {
	aoi := newTestAOI()
	const mover, neighbor int64 = 6201, 6202
	moverLat, moverLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(mover, moverLat, moverLng)
	neighborLat, neighborLng := localLatLng(aoi.config, 700, 0)
	aoi.Insert(neighbor, neighborLat, neighborLng)
	aoi.removeRelationship(mover, neighbor)
	aoi.TakeStats()

	// Move 30m but into a different cell (just over cell boundary).
	dstLat, dstLng := localLatLng(aoi.config, 610, 0)
	delta := aoi.MoveDetailed(mover, dstLat, dstLng)
	if !sliceContains(delta.Entered, neighbor) {
		t.Fatalf("Entered = %v, want to contain %d after cell change", delta.Entered, neighbor)
	}
	stats := aoi.TakeStats()
	if stats.FullEnterScans != 1 {
		t.Fatalf("FullEnterScans = %d, want 1", stats.FullEnterScans)
	}
	if stats.SkippedEnterScans != 0 {
		t.Fatalf("SkippedEnterScans = %d, want 0", stats.SkippedEnterScans)
	}
}

func TestAOIMoveDetailedLeaveDetectionIsExactEvenWhenScanSkipped(t *testing.T) {
	aoi := newTestAOI()
	const mover, neighbor int64 = 6301, 6302
	moverLat, moverLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(mover, moverLat, moverLng)
	neighborLat, neighborLng := localLatLng(aoi.config, 590, 0)
	aoi.Insert(neighbor, neighborLat, neighborLng)
	// Move mover such that neighbor is now > 600m away but still same cell
	// and within threshold from the last enter scan.
	aoi.TakeStats()
	dstLat, dstLng := localLatLng(aoi.config, -20, 0)
	delta := aoi.MoveDetailed(mover, dstLat, dstLng)
	if !sliceContains(delta.Left, neighbor) {
		t.Fatalf("Left = %v, want to contain %d (leave must be exact)", delta.Left, neighbor)
	}
	stats := aoi.TakeStats()
	if stats.LeaveChecks == 0 {
		t.Fatal("LeaveChecks must be > 0 even when enter scan is skipped")
	}
}

func TestAOIInsertSetsEnterScanMarker(t *testing.T) {
	aoi := newTestAOI()
	const p int64 = 6401
	lat, lng := localLatLng(aoi.config, 100, 200)
	aoi.Insert(p, lat, lng)
	pl, ok := aoi.players[p]
	if !ok {
		t.Fatalf("player %d missing", p)
	}
	if !pl.hasEnterScanMarker {
		t.Fatal("Insert should set hasEnterScanMarker = true")
	}
	if pl.lastEnterScanCell != pl.cell {
		t.Fatalf("lastEnterScanCell = %+v, want %+v", pl.lastEnterScanCell, pl.cell)
	}
}

func TestAOISymmetryHoldsAfterSkippedScans(t *testing.T) {
	aoi := newTestAOI()
	const a, b int64 = 6501, 6502
	aLat, aLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(a, aLat, aLng)
	bLat, bLng := localLatLng(aoi.config, 200, 0)
	aoi.Insert(b, bLat, bLng)
	a1Lat, a1Lng := localLatLng(aoi.config, 10, 10)
	aoi.MoveDetailed(a, a1Lat, a1Lng)
	b1Lat, b1Lng := localLatLng(aoi.config, 210, 10)
	aoi.MoveDetailed(b, b1Lat, b1Lng)
	if aoi.IsVisible(a, b) != aoi.IsVisible(b, a) {
		t.Fatal("symmetry broken after skipped scans")
	}
}
```

The existing helper `localLatLng(config AOIConfig, localX, localY float64) (float64, float64)` in `aoi_test.go` already provides the conversion. Replace every `localLatLngVal(aoi.config, x, y)` in the test bodies above with `localLatLng(aoi.config, x, y)` before saving.

Add the `sliceContains` helper (it does not yet exist in `aoi_test.go`):

```go
func sliceContains(s []int64, v int64) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run new tests to verify they fail**

Run: `go test ./internal/game -run 'TestAOIMoveDetailed|TestAOIInsertSetsEnterScanMarker|TestAOISymmetryHoldsAfterSkippedScans' -v`
Expected: FAIL. Movement still runs a full scan unconditionally and skip
counters never increment.

- [ ] **Step 3: Add helper `shouldForceEnterScan`**

In `internal/game/aoi.go`:

```go
func (a *AOIIndex) shouldForceEnterScan(self *aoiPlayer) bool {
	if !self.hasEnterScanMarker {
		return true
	}
	if self.cell != self.lastEnterScanCell {
		return true
	}
	dx := self.localX - self.lastEnterScanX
	dy := self.localY - self.lastEnterScanY
	return dx*dx+dy*dy >= a.config.enterRescanDistanceSquared()
}

func (a *AOIIndex) markEnterScan(self *aoiPlayer) {
	self.lastEnterScanX = self.localX
	self.lastEnterScanY = self.localY
	self.lastEnterScanCell = self.cell
	self.hasEnterScanMarker = true
}
```

- [ ] **Step 4: Update `MoveDetailed` to use bounded-delay logic**

Replace `MoveDetailed` in `internal/game/aoi.go`:

```go
func (a *AOIIndex) MoveDetailed(playerID int64, lat, lng float64) MovementDelta {
	self, exists := a.players[playerID]
	if !exists {
		return MovementDelta{PlayerID: playerID}
	}

	oldVisible := a.visible[playerID]
	oldNeighborIDs := make([]int64, 0, len(oldVisible))
	for nid := range oldVisible {
		oldNeighborIDs = append(oldNeighborIDs, nid)
	}

	a.setPosition(playerID, lat, lng)
	self = a.players[playerID]

	left := a.leaveCheckExistingNeighbors(self, playerID)

	var entered []int64
	if a.shouldForceEnterScan(self) {
		entered = a.enterScanNineCells(self, playerID)
		a.stats.FullEnterScans++
		a.markEnterScan(self)
	} else {
		a.stats.SkippedEnterScans++
	}

	leftSet := make(map[int64]struct{}, len(left))
	for _, id := range left {
		leftSet[id] = struct{}{}
	}
	stable := make([]int64, 0, len(oldNeighborIDs))
	for _, id := range oldNeighborIDs {
		if _, isLeft := leftSet[id]; isLeft {
			continue
		}
		stable = append(stable, id)
	}
	a.stats.StableRelationships += uint64(len(stable))

	return MovementDelta{
		PlayerID: playerID,
		Entered:  entered,
		Left:     left,
		Stable:   stable,
	}
}
```

Note: `setPosition` may have invalidated the `*aoiPlayer` pointer on cell
change (it does not — `setPosition` mutates in place), but re-resolving
`a.players[playerID]` is defensive and free.

- [ ] **Step 5: Update `Insert` and `recalculateRelationships` to refresh the marker**

In `internal/game/aoi.go`, after the existing
`recalculateRelationships` body change in Task 2, set the marker. Replace the
final body:

```go
func (a *AOIIndex) recalculateRelationships(playerID int64) RelationshipChanges {
	self, exists := a.players[playerID]
	if !exists {
		return RelationshipChanges{}
	}
	entered := a.enterScanNineCells(self, playerID)
	a.stats.FullEnterScans++
	a.markEnterScan(self)
	left := a.leaveCheckExistingNeighbors(self, playerID)
	return RelationshipChanges{Entered: entered, Left: left}
}
```

`Insert` already calls `recalculateRelationships`, so the marker is set
through that path. No further `Insert` change required.

- [ ] **Step 6: Run all AOI tests**

Run: `go test ./internal/game -v`
Expected: PASS. New tests pass; all original AOI tests still pass because they
either move across cells, move > 50m, or rely only on leave detection.

- [ ] **Step 7: Commit**

```bash
git add internal/game/aoi.go internal/game/aoi_test.go
git commit -m "feat(aoi): skip enter scan for small same-cell movements"
```

---

### Task 4: Surface New AOI Stats In HubSnapshot And Stats Log

- [ ] Expose the four new AOI counters through `HubSnapshot` and the realtime
  stats log without changing replication behavior.

**Files:**

- Modify: `internal/realtime/stats.go`
- Modify: `internal/realtime/hub.go`
- Modify: `internal/realtime/hub_test.go`
- Modify: `internal/server/stats_test.go`

**Interfaces:**

- Produces:
  - `HubSnapshot.AOIFullEnterScans uint64`,
    `AOISkippedEnterScans uint64`,
    `AOILeaveChecks uint64`,
    `AOIStableRelationships uint64`.
  - `intervalStats` in `hub.go` gains matching `aoiFullEnterScans` /
    `aoiSkippedEnterScans` / `aoiLeaveChecks` / `aoiStableRelationships`
    fields.
  - Stats log line includes new keys (see Step 4 format string).

- [ ] **Step 1: Write a hub snapshot test**

In `internal/realtime/hub_test.go`, find an existing snapshot/replication test
that triggers `logStats` (e.g. `TestHubSnapshotReplicationCounted`). Add a new
focused test alongside it:

```go
func TestHubSnapshotIncludesAOIEnterScanStats(t *testing.T) {
	h, _, advance, snapshot := newManualHubForTest(t)
	defer h.Stop()

	// Drive enough movement to trigger at least one MoveDetailed.
	advance.broadcastTick()
	advance.statsTick()

	snap := snapshot()
	if snap == nil {
		t.Fatal("snapshot was nil")
	}
	// Both counters exist; we only assert reachability through the struct
	// because exact counts depend on the manual scenario.
	_ = snap.AOIFullEnterScans
	_ = snap.AOISkippedEnterScans
	_ = snap.AOILeaveChecks
	_ = snap.AOIStableRelationships
}
```

If `newManualHubForTest` is not exactly that name, use the existing manual-hub
helper from `manual_hub.go`. The point of this test is to assert the four new
fields exist on `HubSnapshot`; compile-time presence is enough.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/realtime -run TestHubSnapshotIncludesAOIEnterScanStats -v`
Expected: FAIL with "snap.AOIFullEnterScans undefined".

- [ ] **Step 3: Add fields to `HubSnapshot`**

In `internal/realtime/stats.go`:

```go
type HubSnapshot struct {
	ConnectedClients      int
	AcceptedInputs        uint64
	SimulationTicks       uint64
	MovedPlayers          uint64
	AOICandidatePairs     uint64
	AOIDistanceChecks     uint64
	AOIFullEnterScans     uint64
	AOISkippedEnterScans  uint64
	AOILeaveChecks        uint64
	AOIStableRelationships uint64
	RelationshipsEntered  uint64
	RelationshipsLeft     uint64
	ReplicationMessages   uint64
	ReplicationRecipients uint64
	ReplicationBytes      uint64
	Builder               BuilderStats
	Dispatcher            DispatcherStats
	AOIDetailedMoveDuration   time.Duration
	CollectibleRecalcDuration time.Duration
	SampledAt             time.Time
}
```

- [ ] **Step 4: Plumb counters through `intervalStats` and `logStats`**

In `internal/realtime/hub.go`, add to `intervalStats`:

```go
type intervalStats struct {
	// ... existing fields ...
	aoiCandidatePairs        uint64
	aoiDistanceChecks        uint64
	aoiRelationshipsEntered  uint64
	aoiRelationshipsLeft     uint64
	aoiFullEnterScans        uint64
	aoiSkippedEnterScans     uint64
	aoiLeaveChecks           uint64
	aoiStableRelationships   uint64
	// ... rest ...
}
```

Update `logStats` (around `hub.go:993`):

```go
aoiStats := h.aoi.TakeStats()
h.stats.aoiCandidatePairs += aoiStats.CandidatePairs
h.stats.aoiDistanceChecks += aoiStats.DistanceChecks
h.stats.aoiRelationshipsEntered += aoiStats.RelationshipsEntered
h.stats.aoiRelationshipsLeft += aoiStats.RelationshipsLeft
h.stats.aoiFullEnterScans += aoiStats.FullEnterScans
h.stats.aoiSkippedEnterScans += aoiStats.SkippedEnterScans
h.stats.aoiLeaveChecks += aoiStats.LeaveChecks
h.stats.aoiStableRelationships += aoiStats.StableRelationships

snap := &HubSnapshot{
	// ... existing assignments ...
	AOIFullEnterScans:      h.stats.aoiFullEnterScans,
	AOISkippedEnterScans:   h.stats.aoiSkippedEnterScans,
	AOILeaveChecks:         h.stats.aoiLeaveChecks,
	AOIStableRelationships: h.stats.aoiStableRelationships,
	// ... rest ...
}
```

Extend the `log.Printf` format string with these keys at the end:

```go
log.Printf(
	"realtime stats ... aoi_full_enter_scans=%d aoi_skipped_enter_scans=%d aoi_leave_checks=%d aoi_stable_relationships=%d",
	// ... existing args ...
	snap.AOIFullEnterScans,
	snap.AOISkippedEnterScans,
	snap.AOILeaveChecks,
	snap.AOIStableRelationships,
)
```

Keep the existing keys; only append the four new ones at the end of the
format string and argument list.

- [ ] **Step 5: Update server stats test if it asserts JSON keys**

In `internal/server/stats_test.go`, search for any test that decodes the stats
JSON and asserts on the `HubSnapshot` keys. If such a test enumerates expected
keys, add the four new keys to the expected set. Otherwise no change.

- [ ] **Step 6: Run realtime and server tests**

Run: `go test ./internal/realtime ./internal/server -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/realtime/stats.go internal/realtime/hub.go internal/realtime/hub_test.go internal/server/stats_test.go
git commit -m "feat(realtime): surface AOI enter-scan stats in HubSnapshot"
```

---

### Task 5: Realtime Behavior Tests For Delayed Enter

- [ ] Verify the bounded-delay semantic at the Hub level: replication can
  delay enter for small same-cell movement, and stable-position fanout still
  works after a skipped scan. (Leave-exactness under skipped scan is covered
  at the AOI level by Task 3's
  `TestAOIMoveDetailedLeaveDetectionIsExactEvenWhenScanSkipped`; reproducing
  it through Hub input-driven movement is awkward because input speed × tick
  granularity would have to land inside the 50m threshold while still pushing
  a neighbor beyond 600m, and that corner is hard to hit through inputs
  without making the test fragile. The AOI-level test already proves the
  invariant.)

**Files:**

- Modify: `internal/realtime/hub_test.go`

**Interfaces:**

- Consumes: existing helpers `newTestHubWithConfig`, `NewTestClient`,
  `mustReceiveInitialization`, `mustReceiveReplicationUpdate`,
  `localLatLng`, `game.InputState`.
- Produces: two Hub-level tests plus two helper loaders and one helper
  world config; no production code changes in this task.

- [ ] **Step 1: Add helper config and loaders**

Add near the other test loaders in `internal/realtime/hub_test.go` (after
`hysteresisPlayerLoader`):

```go
// aoiSkipScanWorldConfig 选择速度 600 m/s 使每个 50ms 仿真 tick 恰好移动 30m。
// 配合 50m EnterRescanDistanceMeters 默认值，一 tick = skipped，二 tick = forced。
func aoiSkipScanWorldConfig() game.Config {
	cfg := testWorldConfig()
	cfg.SpeedMetersPerSecond = 600
	return cfg
}

// aoiDelayedEnterLoader 将 alice 放在 (0,0)、bob 放在 (520,0)，初始 520m 互不可见。
func aoiDelayedEnterLoader() SavedPlayerLoader {
	return func(userID int64) (SavedPlayerLoad, bool) {
		switch userID {
		case 1001:
			lat, lng := localLatLng(0, 0)
			return SavedPlayerLoad{Username: "alice", Lat: lat, Lng: lng, HasPosition: true}, true
		case 1002:
			lat, lng := localLatLng(520, 0)
			return SavedPlayerLoad{Username: "bob", Lat: lat, Lng: lng, HasPosition: true}, true
		}
		return SavedPlayerLoad{}, false
	}
}

// aoiStableFanoutLoader 将 alice (0,0) 和 bob (200,0) 互相可见。
func aoiStableFanoutLoader() SavedPlayerLoader {
	return func(userID int64) (SavedPlayerLoad, bool) {
		switch userID {
		case 1001:
			lat, lng := localLatLng(0, 0)
			return SavedPlayerLoad{Username: "alice", Lat: lat, Lng: lng, HasPosition: true}, true
		case 1002:
			lat, lng := localLatLng(200, 0)
			return SavedPlayerLoad{Username: "bob", Lat: lat, Lng: lng, HasPosition: true}, true
		}
		return SavedPlayerLoad{}, false
	}
}
```

- [ ] **Step 2: Write delayed-enter Hub test**

Add to `internal/realtime/hub_test.go`:

```go
func TestHubReplicationDelayedEnterForSmallSameCellMove(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHubWithConfig(
		aoiSkipScanWorldConfig(), aoiDelayedEnterLoader(), nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 16)
	bob := NewTestClient(1002, 16)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)

	// Tick 1: alice moves +30m east (one 50ms tick at 600 m/s).
	// Distance from her last enter-scan marker (0,0) = 30 < 50m threshold,
	// same cell. Enter scan must be skipped. Even though distance to bob
	// becomes 490m < 500m, bob must NOT enter alice's visibility yet.
	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	broadcasts <- time.Now()
	update := mustReceiveReplicationUpdate(t, alice)
	if update.SelfPosition == nil {
		t.Fatalf("alice should receive selfPosition after moving, got %+v", update)
	}
	if len(update.Entered) != 0 {
		t.Fatalf("expected delayed enter (skipped scan), got entered=%+v", update.Entered)
	}

	// Tick 2: alice continues right; cumulative move is +60m east.
	// Distance from last enter-scan marker (0,0) = 60 >= 50m → force scan.
	// Distance to bob = 460m < 500m → bob enters.
	simulations <- time.Now()
	broadcasts <- time.Now()
	update = mustReceiveReplicationUpdate(t, alice)
	if len(update.Entered) != 1 || update.Entered[0].ID != 1002 {
		t.Fatalf("expected bob entered after threshold crossing, got %+v", update.Entered)
	}
}
```

- [ ] **Step 3: Write stable-fanout Hub test**

```go
func TestHubReplicationStablePositionFanoutAfterSkippedScan(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHubWithConfig(
		aoiSkipScanWorldConfig(), aoiStableFanoutLoader(), nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 16)
	bob := NewTestClient(1002, 16)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)

	// Alice moves +30m east. Same cell, < 50m threshold → skipped scan.
	// Bob remains visible (distance 170m, well within hysteresis).
	// Bob must still receive alice's updated position through the
	// stable-neighbor fanout path.
	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	aliceUpdate := mustReceiveReplicationUpdate(t, alice)
	if aliceUpdate.SelfPosition == nil {
		t.Fatal("alice should receive own selfPosition")
	}
	bobUpdate := mustReceiveReplicationUpdate(t, bob)
	found := false
	for _, p := range bobUpdate.Positions {
		if p.ID == 1001 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bob should receive alice's stable position, got %+v", bobUpdate.Positions)
	}
}
```

- [ ] **Step 4: Run new realtime tests**

Run: `go test ./internal/realtime -run 'TestHubReplication(DelayedEnter|StablePositionFanout)' -v`
Expected: PASS.

- [ ] **Step 5: Run full realtime test suite to catch regressions**

Run: `go test ./internal/realtime -v`
Expected: PASS. Existing scale tests
(`TestAOIThousandClientScenarioDeterministic`,
`TestCollectibleScaleNoAOIRegression`, etc.) may shift relationship counts
because previously-eager enter discovery is now sometimes delayed by one
broadcast tick. If a scale-test assertion fails on exact-count of
relationships-entered, do NOT loosen the assertion blindly; first compare
the new count to what would happen if every mover crossed at least one cell
or 50m within the scenario window. The scale-test movement deltas should be
large enough to force a scan every broadcast — if not, the test is now
covering a different invariant and should be split: keep the existing test
with movements ≥ 50m, add a separate test for sub-threshold movement with
adjusted expected counts.

- [ ] **Step 6: Commit**

```bash
git add internal/realtime/hub_test.go
git commit -m "test(realtime): cover delayed enter and stable fanout under skipped AOI scan"
```

---

### Task 6: Add Direct AOI Movement Benchmark

- [ ] Add a benchmark that lets `go test ./internal/game -bench AOI` produce
  measurable output, matching the spec's required command shape.

**Files:**

- Create: `internal/game/aoi_bench_test.go`

**Interfaces:**

- Produces: `BenchmarkAOIMoveSameCellSmall(b *testing.B)`,
  `BenchmarkAOIMoveCrossCell(b *testing.B)`,
  `BenchmarkAOIMoveBeyondThreshold(b *testing.B)`.

- [ ] **Step 1: Write the benchmark file**

Create `internal/game/aoi_bench_test.go`:

```go
package game

import (
	"math/rand"
	"testing"
)

const benchPlayerCount = 2000

func newBenchAOI(b *testing.B) (*AOIIndex, []int64) {
	b.Helper()
	cfg := AOIConfigFromWorld(testConfig())
	aoi := NewAOIIndex(cfg)
	ids := make([]int64, 0, benchPlayerCount)
	rng := rand.New(rand.NewSource(1))
	// Spread players uniformly across a 6km x 6km square centered on origin.
	const span = 6000.0
	for i := 0; i < benchPlayerCount; i++ {
		x := (rng.Float64() - 0.5) * span
		y := (rng.Float64() - 0.5) * span
		lat, lng := cfg.LocalToLatLng(x, y)
		id := int64(i + 1)
		aoi.Insert(id, lat, lng)
		ids = append(ids, id)
	}
	aoi.TakeStats()
	return aoi, ids
}

func BenchmarkAOIMoveSameCellSmall(b *testing.B) {
	aoi, ids := newBenchAOI(b)
	cfg := aoi.config
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%len(ids)]
		px, py, _ := aoi.LocalPosition(id)
		// Step by 5m east, well below 50m threshold and well within a cell.
		lat, lng := cfg.LocalToLatLng(px+5, py)
		aoi.MoveDetailed(id, lat, lng)
	}
	b.ReportMetric(float64(aoi.TakeStats().SkippedEnterScans)/float64(b.N), "skipped/op")
}

func BenchmarkAOIMoveBeyondThreshold(b *testing.B) {
	aoi, ids := newBenchAOI(b)
	cfg := aoi.config
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%len(ids)]
		px, py, _ := aoi.LocalPosition(id)
		// Step by 60m east, above threshold but typically same cell.
		lat, lng := cfg.LocalToLatLng(px+60, py)
		aoi.MoveDetailed(id, lat, lng)
	}
	b.ReportMetric(float64(aoi.TakeStats().FullEnterScans)/float64(b.N), "full_scans/op")
}

func BenchmarkAOIMoveCrossCell(b *testing.B) {
	aoi, ids := newBenchAOI(b)
	cfg := aoi.config
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%len(ids)]
		px, py, _ := aoi.LocalPosition(id)
		// Step by 700m east, guaranteed cell change.
		lat, lng := cfg.LocalToLatLng(px+700, py)
		aoi.MoveDetailed(id, lat, lng)
	}
	b.ReportMetric(float64(aoi.TakeStats().FullEnterScans)/float64(b.N), "full_scans/op")
}
```

If `testConfig()` is not accessible across files in `package game`, either
inline a minimal `Config{SpawnLat: ..., SpawnLng: ...}` or move the helper.

- [ ] **Step 2: Run benchmarks to confirm they execute**

Run: `go test ./internal/game -run '^$' -bench AOI -benchmem -benchtime=200ms`
Expected: All three benchmarks execute. `BenchmarkAOIMoveSameCellSmall`
reports `skipped/op ≈ 1.00`. `BenchmarkAOIMoveBeyondThreshold` and
`BenchmarkAOIMoveCrossCell` report `full_scans/op ≈ 1.00`.

- [ ] **Step 3: Commit**

```bash
git add internal/game/aoi_bench_test.go
git commit -m "test(aoi): add direct movement benchmarks for skipped vs full scan paths"
```

---

### Task 7: Evidence Report And Handoff Update

- [ ] Produce the benchmark report required by the spec and update the
  handoff document.

**Files:**

- Create: `docs/benchmarks/aoi-incremental-enter-scan.md`
- Modify: `docs/map-walker-handoff.md`

**Interfaces:**

- Consumes: completed Tasks 1–6.
- Produces: a markdown evidence note containing exact commands, before/after
  numbers, and a recommendation against the spec's Decision Rule.

- [ ] **Step 1: Capture before/after numbers**

On a clean working tree (Task 6 just committed), check out the commit before
Task 1 (or just before the `recalculateRelationships` split) to capture the
"before" baseline:

```bash
BEFORE_REF=$(git rev-list -n 1 HEAD~6 || git rev-list -n 1 main || git rev-parse HEAD~6)
git stash -u
git checkout "$BEFORE_REF" -- internal/game/aoi.go internal/realtime/hub.go internal/realtime/stats.go
go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem -count=3 | tee /tmp/aoi-bench-before.txt
git checkout -- internal/game/aoi.go internal/realtime/hub.go internal/realtime/stats.go
git stash pop || true
```

If git stash fails or the codebase is dirty, instead reuse the most recent
benchmark numbers already recorded in
`docs/benchmarks/replication-builder.md` (or the latest
`docs/benchmarks/*.md`) as the "before" baseline and note this in the report.

Now capture the "after" numbers on the current HEAD:

```bash
go test ./internal/game -run '^$' -bench AOI -benchmem -count=3 | tee /tmp/aoi-bench-after-game.txt
go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem -count=3 | tee /tmp/aoi-bench-after-hub.txt
```

- [ ] **Step 2: Write the evidence report**

Create `docs/benchmarks/aoi-incremental-enter-scan.md` with this skeleton, all
fields filled in from the captured output:

```markdown
# AOI Incremental Enter Scan Benchmark

Date: 2026-06-22
Spec: docs/superpowers/specs/2026-06-21-aoi-incremental-enter-scan-design.md
Plan: docs/superpowers/plans/2026-06-22-aoi-incremental-enter-scan.md

## Commands

go test ./internal/game -run '^$' -bench AOI -benchmem -count=3
go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem -count=3

## HubReplication 2000 clients

| metric | before | after | delta |
|---|---|---|---|
| ns/op | ... | ... | ... |
| aoi_detailed_move us | ... | ... | ... |
| candidate_pairs/op | ... | ... | ... |
| distance_checks/op | ... | ... | ... |
| relationships_entered/op | ... | ... | ... |
| relationships_left/op | ... | ... | ... |
| full_enter_scans/op | n/a | ... | ... |
| skipped_enter_scans/op | n/a | ... | ... |
| msgs/op | ... | ... | ... |
| bytes/op | ... | ... | ... |
| allocs/op | ... | ... | ... |

## Direct AOI Movement Benchmarks

| benchmark | ns/op | allocs/op | skipped/op or full_scans/op |
|---|---|---|---|
| BenchmarkAOIMoveSameCellSmall | ... | ... | skipped ≈ 1.00 |
| BenchmarkAOIMoveBeyondThreshold | ... | ... | full ≈ 1.00 |
| BenchmarkAOIMoveCrossCell | ... | ... | full ≈ 1.00 |

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
- [ ] Skipped scan counts are low.
  → Movement patterns are crossing cells / thresholds often; analyze workload.
- [ ] Collectible visibility becomes the next visible cost.
  → Optimize recalcCollectibleVisibility separately.

Check exactly one box and write the recommended next plan in one sentence.
```

- [ ] **Step 3: Update `docs/map-walker-handoff.md`**

Per AGENTS.md "每次完成一个 plan 后只保留这个 phase 的详情"，replace the
previous "most recently completed plan" pointer with this plan and add a short
section describing:

- `EnterRescanDistanceMeters` default 50m.
- `MoveDetailed` now skips the nine-cell enter scan for small same-cell
  movement; leave detection remains exact.
- `AOIStats` now reports `FullEnterScans`, `SkippedEnterScans`, `LeaveChecks`,
  `StableRelationships`.
- New `HubSnapshot` fields mirror those four counters.

Remove the equivalent details for whichever phase was previously "most
recent", as the handoff is supposed to stay ~100 lines.

- [ ] **Step 4: Verify final test suite is green**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add docs/benchmarks/aoi-incremental-enter-scan.md docs/map-walker-handoff.md
git commit -m "docs(aoi): record incremental enter scan benchmark and update handoff"
```

---

## Final Verification

- [ ] Run `go test ./internal/game -v`.
- [ ] Run `go test ./internal/realtime -v`.
- [ ] Run `go test ./internal/server -v`.
- [ ] Run `go test ./...` and confirm no regressions outside the spec.
- [ ] Run `go test ./internal/game -run '^$' -bench AOI -benchmem`.
- [ ] Run `go test ./internal/realtime -run '^$' -bench BenchmarkHubReplication -benchmem`.
- [ ] Confirm `docs/benchmarks/aoi-incremental-enter-scan.md` exists with
  filled-in tables and exactly one Decision box checked.
- [ ] Confirm `docs/map-walker-handoff.md` reflects only the current phase's
  details.

Do not claim capacity improvement unless `BenchmarkHubReplication` ns/op drops
or `aoi_detailed_move us` drops with `skipped_enter_scans/op > 0`. If
neither holds, record the result honestly and recommend the next candidate
from the Decision Rule.
