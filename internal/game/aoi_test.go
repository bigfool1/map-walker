package game

import (
	"math"
	"testing"
)

func TestAOIOriginConversionAndCellCoordinates(t *testing.T) {
	config := AOIConfigFromWorld(testConfig())

	localX, localY := config.latLngToLocal(config.OriginLat, config.OriginLng)
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
	roundTripX, roundTripY := config.latLngToLocal(lat, lng)
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

	aoi.Insert("alice", originLat, originLng)
	if cell, ok := aoi.Cell("alice"); !ok || cell != (CellCoord{0, 0}) {
		t.Fatalf("alice initial cell = %+v, want (0, 0)", cell)
	}

	lat, lng := aoi.config.LocalToLatLng(100, 100)
	aoi.Move("alice", lat, lng)
	if cell, ok := aoi.Cell("alice"); !ok || cell != (CellCoord{0, 0}) {
		t.Fatalf("alice same-cell move cell = %+v, want (0, 0)", cell)
	}

	lat, lng = aoi.config.LocalToLatLng(700, 50)
	aoi.Move("alice", lat, lng)
	if cell, ok := aoi.Cell("alice"); !ok || cell != (CellCoord{1, 0}) {
		t.Fatalf("alice cross-cell move cell = %+v, want (1, 0)", cell)
	}
}

func TestAOINineCellCandidateCoverageAndDistanceFiltering(t *testing.T) {
	aoi := newTestAOI()

	centerLat, centerLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("center", centerLat, centerLng)

	nearLat, nearLng := localLatLng(aoi.config, -400, 0)
	changes := aoi.Insert("near-neighbor", nearLat, nearLng)
	if !slicesEqual(changes.Entered, []string{"center"}) {
		t.Fatalf("near-neighbor entered = %+v, want [center]", changes.Entered)
	}

	farLat, farLng := localLatLng(aoi.config, -550, 0)
	aoi.Insert("far-neighbor", farLat, farLng)
	if aoi.isSymmetricVisible("center", "far-neighbor") {
		t.Fatal("expected far-neighbor at 550m to remain invisible")
	}

	outsideLat, outsideLng := localLatLng(aoi.config, -1200, 0)
	aoi.Insert("outside-grid", outsideLat, outsideLng)
	recalc := aoi.RecalculateRelationships("center")
	if contains(recalc.Entered, "outside-grid") {
		t.Fatal("expected outside-grid not to enter from nine-cell lookup")
	}
}

func TestAOIExactFiveHundredMeterEntry(t *testing.T) {
	aoi := newTestAOI()

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("alice", aliceLat, aliceLng)
	lat, lng := localLatLng(aoi.config, 500, 0)
	changes := aoi.Insert("bob", lat, lng)

	if !slicesEqual(changes.Entered, []string{"alice"}) {
		t.Fatalf("bob entered = %+v, want [alice]", changes.Entered)
	}
	if !aoi.isSymmetricVisible("alice", "bob") {
		t.Fatal("expected symmetric visibility at exactly 500m")
	}
}

func TestAOIHysteresisRetentionBetweenFiveHundredAndSixHundredMeters(t *testing.T) {
	aoi := newTestAOI()

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("alice", aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 400, 0)
	aoi.Insert("bob", bobLat, bobLng)
	if !aoi.isSymmetricVisible("alice", "bob") {
		t.Fatal("expected initial visibility")
	}

	bobLat, bobLng = localLatLng(aoi.config, 550, 0)
	changes := aoi.Move("bob", bobLat, bobLng)
	if len(changes.Left) != 0 {
		t.Fatalf("expected hysteresis retention, got left %+v", changes.Left)
	}
	if !aoi.isSymmetricVisible("alice", "bob") {
		t.Fatal("expected relationship to remain visible in hysteresis band")
	}

	// New pair in hysteresis band must not enter.
	newAOI := newTestAOI()
	carolLat, carolLng := localLatLng(newAOI.config, 0, 0)
	newAOI.Insert("carol", carolLat, carolLng)
	daveLat, daveLng := localLatLng(newAOI.config, 550, 0)
	carolChanges := newAOI.Insert("dave", daveLat, daveLng)
	if len(carolChanges.Entered) != 0 {
		t.Fatalf("expected no entry between 500m and 600m, got %+v", carolChanges.Entered)
	}
	if newAOI.isSymmetricVisible("carol", "dave") {
		t.Fatal("expected dave to remain invisible to carol")
	}
}

func TestAOIRemovalBeyondSixHundredMeters(t *testing.T) {
	aoi := newTestAOI()

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("alice", aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 400, 0)
	aoi.Insert("bob", bobLat, bobLng)

	bobLat, bobLng = localLatLng(aoi.config, 599, 0)
	changes := aoi.Move("bob", bobLat, bobLng)
	if len(changes.Left) != 0 {
		t.Fatalf("expected visibility just inside 600m, got left %+v", changes.Left)
	}

	bobLat, bobLng = localLatLng(aoi.config, 601, 0)
	changes = aoi.Move("bob", bobLat, bobLng)
	if !slicesEqual(changes.Left, []string{"alice"}) {
		t.Fatalf("bob left = %+v, want [alice]", changes.Left)
	}
	if aoi.isSymmetricVisible("alice", "bob") {
		t.Fatal("expected relationship removed beyond 600m")
	}
}

func TestAOISymmetricAddAndRemove(t *testing.T) {
	aoi := newTestAOI()

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("alice", aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 100, 0)
	aoi.Insert("bob", bobLat, bobLng)

	if !aoi.isSymmetricVisible("alice", "bob") {
		t.Fatal("expected symmetric add")
	}

	bobLat, bobLng = localLatLng(aoi.config, 700, 0)
	aoi.Move("bob", bobLat, bobLng)

	if aoi.isSymmetricVisible("alice", "bob") {
		t.Fatal("expected symmetric remove")
	}
	if contains(aoi.VisibleNeighbors("alice"), "bob") || contains(aoi.VisibleNeighbors("bob"), "alice") {
		t.Fatal("expected both visibility sets to drop the relationship")
	}
}

func TestAOIStationaryPeerReceivesSymmetricChanges(t *testing.T) {
	aoi := newTestAOI()

	stationaryLat, stationaryLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("stationary", stationaryLat, stationaryLng)
	movingLat, movingLng := localLatLng(aoi.config, 700, 0)
	aoi.Insert("moving", movingLat, movingLng)

	movingLat, movingLng = localLatLng(aoi.config, 300, 0)
	aoi.Move("moving", movingLat, movingLng)

	if !aoi.isSymmetricVisible("stationary", "moving") {
		t.Fatal("expected stationary peer to gain visibility when moving peer enters")
	}
	if !contains(aoi.VisibleNeighbors("stationary"), "moving") {
		t.Fatalf("stationary neighbors = %+v, want moving", aoi.VisibleNeighbors("stationary"))
	}

	movingLat, movingLng = localLatLng(aoi.config, 700, 0)
	aoi.Move("moving", movingLat, movingLng)

	if aoi.isSymmetricVisible("stationary", "moving") {
		t.Fatal("expected stationary peer to lose visibility when moving peer leaves")
	}
}

func TestAOIDuplicateProcessingIsIdempotent(t *testing.T) {
	aoi := newTestAOI()

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("alice", aliceLat, aliceLng)
	bobStartLat, bobStartLng := localLatLng(aoi.config, 700, 0)
	aoi.Insert("bob", bobStartLat, bobStartLng)

	aliceLat, aliceLng = localLatLng(aoi.config, 250, 0)
	bobLat, bobLng := localLatLng(aoi.config, 450, 0)
	aliceChanges := aoi.Move("alice", aliceLat, aliceLng)
	bobChanges := aoi.Move("bob", bobLat, bobLng)

	if !slicesEqual(aliceChanges.Entered, []string{"bob"}) {
		t.Fatalf("alice entered = %+v, want [bob]", aliceChanges.Entered)
	}
	if len(bobChanges.Entered) != 0 {
		t.Fatalf("bob should not report duplicate entry, got %+v", bobChanges.Entered)
	}

	secondAlice := aoi.RecalculateRelationships("alice")
	secondBob := aoi.RecalculateRelationships("bob")
	if len(secondAlice.Entered) != 0 || len(secondAlice.Left) != 0 {
		t.Fatalf("second alice recalc = %+v, want no changes", secondAlice)
	}
	if len(secondBob.Entered) != 0 || len(secondBob.Left) != 0 {
		t.Fatalf("second bob recalc = %+v, want no changes", secondBob)
	}
}

func TestAOILargeMoveChecksExistingNeighborsOutsideNineCells(t *testing.T) {
	aoi := newTestAOI()

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("alice", aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 400, 0)
	aoi.Insert("bob", bobLat, bobLng)
	if !aoi.isSymmetricVisible("alice", "bob") {
		t.Fatal("expected initial visibility")
	}

	// Bob jumps far enough that alice is no longer in bob's nine Cells.
	bobLat, bobLng = localLatLng(aoi.config, 5000, 0)
	changes := aoi.Move("bob", bobLat, bobLng)

	if !slicesEqual(changes.Left, []string{"alice"}) {
		t.Fatalf("bob left = %+v, want [alice]", changes.Left)
	}
	if aoi.isSymmetricVisible("alice", "bob") {
		t.Fatal("expected stale relationship removed after large move")
	}
}

func TestAOIRemovePlayerClearsRelationships(t *testing.T) {
	aoi := newTestAOI()

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("alice", aliceLat, aliceLng)
	bobLat, bobLng := localLatLng(aoi.config, 100, 0)
	aoi.Insert("bob", bobLat, bobLng)
	carolLat, carolLng := localLatLng(aoi.config, 0, 150)
	aoi.Insert("carol", carolLat, carolLng)

	changes := aoi.Remove("bob")
	if !slicesEqual(changes.Left, []string{"alice", "carol"}) {
		t.Fatalf("remove left = %+v, want [alice carol]", changes.Left)
	}
	if aoi.HasPlayer("bob") {
		t.Fatal("expected bob to be removed from index")
	}
	if contains(aoi.VisibleNeighbors("alice"), "bob") || contains(aoi.VisibleNeighbors("carol"), "bob") {
		t.Fatal("expected former neighbors to drop bob")
	}
}

func TestAOIInsertMoveRemoveLifecycle(t *testing.T) {
	aoi := newTestAOI()

	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	if changes := aoi.Insert("alice", aliceLat, aliceLng); len(changes.Entered) != 0 {
		t.Fatalf("solo insert entered = %+v, want none", changes.Entered)
	}
	if !aoi.HasPlayer("alice") {
		t.Fatal("expected alice in index after insert")
	}

	lat, lng := localLatLng(aoi.config, 50, 0)
	if changes := aoi.Move("alice", lat, lng); len(changes.Entered) != 0 || len(changes.Left) != 0 {
		t.Fatalf("solo move changes = %+v, want none", changes)
	}

	if changes := aoi.Remove("alice"); len(changes.Left) != 0 {
		t.Fatalf("solo remove left = %+v, want none", changes.Left)
	}
	if aoi.HasPlayer("alice") {
		t.Fatal("expected alice removed")
	}
}

func newTestAOI() *AOIIndex {
	return NewAOIIndex(AOIConfigFromWorld(testConfig()))
}

func localLatLng(config AOIConfig, localX, localY float64) (float64, float64) {
	return config.LocalToLatLng(localX, localY)
}

func (a *AOIIndex) isSymmetricVisible(playerA, playerB string) bool {
	return a.IsVisible(playerA, playerB) && a.IsVisible(playerB, playerA)
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestAOIDistanceUsesSquaredEuclideanMeters(t *testing.T) {
	aoi := newTestAOI()
	aLat, aLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("a", aLat, aLng)
	bLat, bLng := localLatLng(aoi.config, 300, 400)
	aoi.Insert("b", bLat, bLng)

	dist := math.Sqrt(aoi.distanceSquared(aoi.players["a"], aoi.players["b"]))
	if !almostEqualLocal(dist, 500) {
		t.Fatalf("distance = %v, want 500", dist)
	}
}

func TestAOIVisibleRelationshipPairs(t *testing.T) {
	aoi := newTestAOI()
	aliceLat, aliceLng := localLatLng(aoi.config, 0, 0)
	aoi.Insert("alice", aliceLat, aliceLng)
	if pairs := aoi.VisibleRelationshipPairs(); pairs != 0 {
		t.Fatalf("solo pairs=%d want 0", pairs)
	}

	bobLat, bobLng := localLatLng(aoi.config, 100, 0)
	aoi.Insert("bob", bobLat, bobLng)
	if pairs := aoi.VisibleRelationshipPairs(); pairs != 1 {
		t.Fatalf("pair count=%d want 1", pairs)
	}

	carolLat, carolLng := localLatLng(aoi.config, 700, 0)
	aoi.Insert("carol", carolLat, carolLng)
	if pairs := aoi.VisibleRelationshipPairs(); pairs != 1 {
		t.Fatalf("non-visible third player pairs=%d want 1", pairs)
	}
}
