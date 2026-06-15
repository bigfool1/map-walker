package game

import (
	"math"
	"testing"
)

func TestAOIOriginConversionAndCellCoordinates(t *testing.T) {
	config := AOIConfigFromWorld(testConfig())

	localX, localY := config.LatLngToLocal(config.OriginLat, config.OriginLng)
	if localX != 0 || localY != 0 {
		t.Fatalf("origin local coords = (%v, %v), want (0, 0)", localX, localY)
	}
	if cell := config.localToCell(localX, localY); cell != (CellCoord{0, 0}) {
		t.Fatalf("origin cell = %+v, want (0, 0)", cell)
	}

	positiveCell := config.localToCell(650, 1250)
	if positiveCell != (CellCoord{1, 2}) {
		t.Fatalf("positive cell = %+v, want (1, 2)", positiveCell)
	}

	negativeCell := config.localToCell(-650, -1250)
	if negativeCell != (CellCoord{-2, -3}) {
		t.Fatalf("negative cell = %+v, want (-2, -3)", negativeCell)
	}

	lat, lng := config.LocalToLatLng(300, -400)
	roundTripX, roundTripY := config.LatLngToLocal(lat, lng)
	if !almostEqualLocal(roundTripX, 300) || !almostEqualLocal(roundTripY, -400) {
		t.Fatalf("round trip local coords = (%v, %v), want (300, -400)", roundTripX, roundTripY)
	}
}

func almostEqualLocal(a, b float64) bool {
	return math.Abs(a-b) < 0.01
}

func TestAOIMoveWithinAndAcrossCells(t *testing.T) {
	aoi := newTestAOI()
	originLat, originLng := testConfig().SpawnLat, testConfig().SpawnLng

	const alice int64 = 1001
	aoi.Insert(alice, originLat, originLng)
	if cell, ok := aoi.Cell(alice); !ok || cell != (CellCoord{0, 0}) {
		t.Fatalf("alice initial cell = %+v, want (0, 0)", cell)
	}

	lat, lng := aoi.config.LocalToLatLng(100, 100)
	aoi.Move(alice, lat, lng)
	if cell, ok := aoi.Cell(alice); !ok || cell != (CellCoord{0, 0}) {
		t.Fatalf("alice same-cell move cell = %+v, want (0, 0)", cell)
	}

	lat, lng = aoi.config.LocalToLatLng(700, 50)
	aoi.Move(alice, lat, lng)
	if cell, ok := aoi.Cell(alice); !ok || cell != (CellCoord{1, 0}) {
		t.Fatalf("alice cross-cell move cell = %+v, want (1, 0)", cell)
	}
}

func TestAOINineCellCandidateCoverageAndDistanceFiltering(t *testing.T) {
	aoi := newTestAOI()

	const center, near, far, outside int64 = 2001, 2002, 2003, 2004

	centerLat, centerLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(center, centerLat, centerLng)

	nearLat, nearLng := localLatLng(aoi.config, -400, 0)
	changes := aoi.Insert(near, nearLat, nearLng)
	if !sliceSetEqual(changes.Entered, []int64{center}) {
		t.Fatalf("near-neighbor entered = %+v, want [%d]", changes.Entered, center)
	}

	farLat, farLng := localLatLng(aoi.config, -550, 0)
	aoi.Insert(far, farLat, farLng)
	if aoi.isSymmetricVisible(center, far) {
		t.Fatal("expected far-neighbor at 550m to remain invisible")
	}

	outsideLat, outsideLng := localLatLng(aoi.config, -1200, 0)
	aoi.Insert(outside, outsideLat, outsideLng)
	recalc := aoi.RecalculateRelationships(center)
	if contains(recalc.Entered, outside) {
		t.Fatal("expected outside-grid not to enter from nine-cell lookup")
	}
}

func TestAOIExactFiveHundredMeterEntry(t *testing.T) {
	aoi := newTestAOI()

	const alice, bob int64 = 1001, 1002

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(alice, aliceLat, aliceLng)
	lat, lng := localLatLng(aoi.config, 500, 0)
	changes := aoi.Insert(bob, lat, lng)

	if !sliceSetEqual(changes.Entered, []int64{alice}) {
		t.Fatalf("bob entered = %+v, want [%d]", changes.Entered, alice)
	}
	if !aoi.isSymmetricVisible(alice, bob) {
		t.Fatal("expected symmetric visibility at exactly 500m")
	}
}

func TestAOIHysteresisRetentionBetweenFiveHundredAndSixHundredMeters(t *testing.T) {
	aoi := newTestAOI()

	const alice, bob, carol, dave int64 = 1001, 1002, 1007, 1008

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(alice, aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 400, 0)
	aoi.Insert(bob, bobLat, bobLng)
	if !aoi.isSymmetricVisible(alice, bob) {
		t.Fatal("expected initial visibility")
	}

	bobLat, bobLng = localLatLng(aoi.config, 550, 0)
	changes := aoi.Move(bob, bobLat, bobLng)
	if len(changes.Left) != 0 {
		t.Fatalf("expected hysteresis retention, got left %+v", changes.Left)
	}
	if !aoi.isSymmetricVisible(alice, bob) {
		t.Fatal("expected relationship to remain visible in hysteresis band")
	}

	// New pair in hysteresis band must not enter.
	newAOI := newTestAOI()
	carolLat, carolLng := localLatLng(newAOI.config, 0, 0)
	newAOI.Insert(carol, carolLat, carolLng)
	daveLat, daveLng := localLatLng(newAOI.config, 550, 0)
	carolChanges := newAOI.Insert(dave, daveLat, daveLng)
	if len(carolChanges.Entered) != 0 {
		t.Fatalf("expected no entry between 500m and 600m, got %+v", carolChanges.Entered)
	}
	if newAOI.isSymmetricVisible(carol, dave) {
		t.Fatal("expected dave to remain invisible to carol")
	}
}

func TestAOIRemovalBeyondSixHundredMeters(t *testing.T) {
	aoi := newTestAOI()

	const alice, bob int64 = 1001, 1002

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(alice, aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 400, 0)
	aoi.Insert(bob, bobLat, bobLng)

	bobLat, bobLng = localLatLng(aoi.config, 599, 0)
	changes := aoi.Move(bob, bobLat, bobLng)
	if len(changes.Left) != 0 {
		t.Fatalf("expected visibility just inside 600m, got left %+v", changes.Left)
	}

	bobLat, bobLng = localLatLng(aoi.config, 601, 0)
	changes = aoi.Move(bob, bobLat, bobLng)
	if !sliceSetEqual(changes.Left, []int64{alice}) {
		t.Fatalf("bob left = %+v, want [%d]", changes.Left, alice)
	}
	if aoi.isSymmetricVisible(alice, bob) {
		t.Fatal("expected relationship removed beyond 600m")
	}
}

func TestAOIMultiCellCandidatesNoOmissionOrDuplication(t *testing.T) {
	aoi := newTestAOI()

	const center, east, north, west int64 = 2001, 2002, 2003, 2004

	centerLat, centerLng := localLatLng(aoi.config, 300, 300)
	aoi.Insert(center, centerLat, centerLng)

	eastLat, eastLng := localLatLng(aoi.config, 650, 300)
	aoi.Insert(east, eastLat, eastLng)
	northLat, northLng := localLatLng(aoi.config, 300, 700)
	aoi.Insert(north, northLat, northLng)
	westLat, westLng := localLatLng(aoi.config, -200, 300)
	aoi.Insert(west, westLat, westLng)

	wantNeighbors := []int64{east, north, west}
	if !sliceSetEqual(aoi.VisibleNeighbors(center), wantNeighbors) {
		t.Fatalf("center neighbors = %+v, want %+v", aoi.VisibleNeighbors(center), wantNeighbors)
	}

	recalc := aoi.RecalculateRelationships(center)
	if len(recalc.Entered) != 0 || len(recalc.Left) != 0 {
		t.Fatalf("second recalc = %+v, want no changes", recalc)
	}
	if pairs := aoi.VisibleRelationshipPairs(); pairs != 3 {
		t.Fatalf("relationship pairs = %d, want 3", pairs)
	}
	for _, neighborID := range wantNeighbors {
		if !aoi.isSymmetricVisible(center, neighborID) {
			t.Fatalf("expected symmetric visibility between center and %d", neighborID)
		}
	}
}

func TestAOISymmetricAddAndRemove(t *testing.T) {
	aoi := newTestAOI()

	const alice, bob int64 = 1001, 1002

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(alice, aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 100, 0)
	aoi.Insert(bob, bobLat, bobLng)

	if !aoi.isSymmetricVisible(alice, bob) {
		t.Fatal("expected symmetric add")
	}

	bobLat, bobLng = localLatLng(aoi.config, 700, 0)
	aoi.Move(bob, bobLat, bobLng)

	if aoi.isSymmetricVisible(alice, bob) {
		t.Fatal("expected symmetric remove")
	}
	if contains(aoi.VisibleNeighbors(alice), bob) || contains(aoi.VisibleNeighbors(bob), alice) {
		t.Fatal("expected both visibility sets to drop the relationship")
	}
}

func TestAOIStationaryPeerReceivesSymmetricChanges(t *testing.T) {
	aoi := newTestAOI()

	const stationary, moving int64 = 5001, 5002

	stationaryLat, stationaryLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(stationary, stationaryLat, stationaryLng)
	movingLat, movingLng := localLatLng(aoi.config, 700, 0)
	aoi.Insert(moving, movingLat, movingLng)

	movingLat, movingLng = localLatLng(aoi.config, 300, 0)
	aoi.Move(moving, movingLat, movingLng)

	if !aoi.isSymmetricVisible(stationary, moving) {
		t.Fatal("expected stationary peer to gain visibility when moving peer enters")
	}
	if !contains(aoi.VisibleNeighbors(stationary), moving) {
		t.Fatalf("stationary neighbors = %+v, want moving", aoi.VisibleNeighbors(stationary))
	}

	movingLat, movingLng = localLatLng(aoi.config, 700, 0)
	aoi.Move(moving, movingLat, movingLng)

	if aoi.isSymmetricVisible(stationary, moving) {
		t.Fatal("expected stationary peer to lose visibility when moving peer leaves")
	}
}

func TestAOIDuplicateProcessingIsIdempotent(t *testing.T) {
	aoi := newTestAOI()

	const alice, bob int64 = 1001, 1002

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(alice, aliceLat, aliceLng)
	bobStartLat, bobStartLng := localLatLng(aoi.config, 700, 0)
	aoi.Insert(bob, bobStartLat, bobStartLng)

	aliceLat, aliceLng = localLatLng(aoi.config, 250, 0)
	bobLat, bobLng := localLatLng(aoi.config, 450, 0)
	aliceChanges := aoi.Move(alice, aliceLat, aliceLng)
	bobChanges := aoi.Move(bob, bobLat, bobLng)

	if !sliceSetEqual(aliceChanges.Entered, []int64{bob}) {
		t.Fatalf("alice entered = %+v, want [%d]", aliceChanges.Entered, bob)
	}
	if len(bobChanges.Entered) != 0 {
		t.Fatalf("bob should not report duplicate entry, got %+v", bobChanges.Entered)
	}

	secondAlice := aoi.RecalculateRelationships(alice)
	secondBob := aoi.RecalculateRelationships(bob)
	if len(secondAlice.Entered) != 0 || len(secondAlice.Left) != 0 {
		t.Fatalf("second alice recalc = %+v, want no changes", secondAlice)
	}
	if len(secondBob.Entered) != 0 || len(secondBob.Left) != 0 {
		t.Fatalf("second bob recalc = %+v, want no changes", secondBob)
	}
}

func TestAOILargeMoveChecksExistingNeighborsOutsideNineCells(t *testing.T) {
	aoi := newTestAOI()

	const alice, bob int64 = 1001, 1002

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(alice, aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 400, 0)
	aoi.Insert(bob, bobLat, bobLng)
	if !aoi.isSymmetricVisible(alice, bob) {
		t.Fatal("expected initial visibility")
	}

	// Bob jumps far enough that alice is no longer in bob's nine Cells.
	bobLat, bobLng = localLatLng(aoi.config, 5000, 0)
	changes := aoi.Move(bob, bobLat, bobLng)

	if !sliceSetEqual(changes.Left, []int64{alice}) {
		t.Fatalf("bob left = %+v, want [%d]", changes.Left, alice)
	}
	if aoi.isSymmetricVisible(alice, bob) {
		t.Fatal("expected stale relationship removed after large move")
	}
}

func TestAOIRemovePlayerClearsRelationships(t *testing.T) {
	aoi := newTestAOI()

	const alice, bob, carol int64 = 1001, 1002, 1007

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(alice, aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 100, 0)
	aoi.Insert(bob, bobLat, bobLng)
	carolLat, carolLng := localLatLng(aoi.config, 0, 150)
	aoi.Insert(carol, carolLat, carolLng)

	changes := aoi.Remove(bob)
	if !slicesEqualInt64(changes.Left, []int64{alice, carol}) {
		t.Fatalf("remove left = %+v, want [%d %d]", changes.Left, alice, carol)
	}
	if aoi.HasPlayer(bob) {
		t.Fatal("expected bob to be removed from index")
	}
	if contains(aoi.VisibleNeighbors(alice), bob) || contains(aoi.VisibleNeighbors(carol), bob) {
		t.Fatal("expected former neighbors to drop bob")
	}
}

func TestAOIInsertMoveRemoveLifecycle(t *testing.T) {
	aoi := newTestAOI()

	const alice int64 = 1001

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	if changes := aoi.Insert(alice, aliceLat, aliceLng); len(changes.Entered) != 0 {
		t.Fatalf("solo insert entered = %+v, want none", changes.Entered)
	}
	if !aoi.HasPlayer(alice) {
		t.Fatal("expected alice in index after insert")
	}

	lat, lng := localLatLng(aoi.config, 50, 0)
	if changes := aoi.Move(alice, lat, lng); len(changes.Entered) != 0 || len(changes.Left) != 0 {
		t.Fatalf("solo move changes = %+v, want none", changes)
	}

	if changes := aoi.Remove(alice); len(changes.Left) != 0 {
		t.Fatalf("solo remove left = %+v, want none", changes.Left)
	}
	if aoi.HasPlayer(alice) {
		t.Fatal("expected alice removed")
	}
}

func TestAOIQueryPlayerIDsNearPoint(t *testing.T) {
	aoi := newTestAOI()
	originLat, originLng := testConfig().SpawnLat, testConfig().SpawnLng

	// 在原点插入一个玩家
	aoi.Insert(1001, originLat, originLng)

	// 500m 外插入另一个玩家
	lat500, lng500 := localLatLng(aoi.config, 500, 0)
	aoi.Insert(1002, lat500, lng500)

	// 1200m 外插入第三个玩家（不同 cell）
	lat1200, lng1200 := localLatLng(aoi.config, 1200, 0)
	aoi.Insert(1003, lat1200, lng1200)

	// 查询原点附近的玩家（九格扫描）
	nearby := aoi.QueryPlayerIDsNearPoint(originLat, originLng)
	if !contains(nearby, 1001) {
		t.Fatal("expected player 1001 at origin to be found")
	}
	if !contains(nearby, 1002) {
		t.Fatal("expected player 1002 at 500m to be in nine-cell scan")
	}
	if contains(nearby, 1003) {
		t.Fatal("player 1003 at 1200m should not be in nine-cell scan from origin")
	}
}

func newTestAOI() *AOIIndex {
	return NewAOIIndex(AOIConfigFromWorld(testConfig()))
}

func localLatLng(config AOIConfig, localX, localY float64) (float64, float64) {
	return config.LocalToLatLng(localX, localY)
}

func (a *AOIIndex) isSymmetricVisible(playerA, playerB int64) bool {
	return a.IsVisible(playerA, playerB) && a.IsVisible(playerB, playerA)
}

func sliceSetEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int64]struct{}, len(a))
	for _, value := range a {
		seen[value] = struct{}{}
	}
	for _, value := range b {
		if _, ok := seen[value]; !ok {
			return false
		}
	}
	return true
}

func contains(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestAOIDistanceUsesSquaredEuclideanMeters(t *testing.T) {
	aoi := newTestAOI()
	const a, b int64 = 9001, 9002
	aLat, aLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(a, aLat, aLng)
	bLat, bLng := localLatLng(aoi.config, 300, 400)
	aoi.Insert(b, bLat, bLng)

	dist := math.Sqrt(aoi.distanceSquared(aoi.players[a], aoi.players[b]))
	if !almostEqualLocal(dist, 500) {
		t.Fatalf("distance = %v, want 500", dist)
	}
}

func TestAOIVisibleRelationshipPairs(t *testing.T) {
	aoi := newTestAOI()
	const alice, bob, carol int64 = 1001, 1002, 1007
	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert(alice, aliceLat, aliceLng)
	if pairs := aoi.VisibleRelationshipPairs(); pairs != 0 {
		t.Fatalf("solo pairs=%d want 0", pairs)
	}

	bobLat, bobLng := localLatLng(aoi.config, 100, 0)
	aoi.Insert(bob, bobLat, bobLng)
	if pairs := aoi.VisibleRelationshipPairs(); pairs != 1 {
		t.Fatalf("pair count=%d want 1", pairs)
	}

	carolLat, carolLng := localLatLng(aoi.config, 700, 0)
	aoi.Insert(carol, carolLat, carolLng)
	if pairs := aoi.VisibleRelationshipPairs(); pairs != 1 {
		t.Fatalf("non-visible third player pairs=%d want 1", pairs)
	}
}
