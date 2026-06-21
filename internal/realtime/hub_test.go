package realtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"math/rand/v2"

	"map-walker/internal/game"
)

func TestHubRegisterSendsInitializationWithoutStaticReplication(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	if !hub.Register(alice) {
		t.Fatal("register failed")
	}
	self, visible := mustReceiveInitialization(t, alice)
	if self.Player.ID != 1001 || len(visible.Players) != 0 {
		t.Fatalf("unexpected initialization: self=%+v visible=%+v", self, visible)
	}
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
}

func TestHubSimulationDoesNotBroadcastUntilBroadcastTick(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	update := mustReceiveReplicationUpdate(t, alice)
	if update.Tick != 1 || update.SelfPosition == nil {
		t.Fatalf("unexpected movement replication: %+v", update)
	}
}

func TestHubEmptyBroadcastTickSendsNothing(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
}

func TestHubDisconnectAppearsInNextReplication(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	joined := mustReceiveReplicationUpdate(t, alice)
	if len(joined.Entered) != 1 || joined.Entered[0].ID != 1002 {
		t.Fatalf("expected bob entered for alice, got %+v", joined)
	}
	assertNoMessage(t, bob)

	hub.Unregister(bob)
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, alice)
	if len(update.LeftPlayerIDs) != 1 || update.LeftPlayerIDs[0] != 1002 {
		t.Fatalf("unexpected removals: %+v", update.LeftPlayerIDs)
	}
}

func TestHubRestoresOfflinePlayerAtSavedPosition(t *testing.T) {
	savedAppearance := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	loader := SavedPlayerLoader(func(userID int64) (SavedPlayerLoad, bool) {
		if userID != 1001 {
			return SavedPlayerLoad{}, false
		}
		return SavedPlayerLoad{
			Lat:         31.5,
			Lng:         121.5,
			HasPosition: true,
			Username:    "Alice",
			Appearance:  savedAppearance,
		}, true
	})

	hub, _, _, _ := newTestHubWithLoader(loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	self, visible := mustReceiveInitialization(t, alice)
	if len(visible.Players) != 0 {
		t.Fatalf("unexpected visible players: %+v", visible)
	}
	if self.Player.Lat != 31.5 || self.Player.Lng != 121.5 {
		t.Fatalf("expected saved position, got %+v", self.Player)
	}
	if self.Player.Appearance != savedAppearance {
		t.Fatalf("expected saved appearance, got %+v", self.Player.Appearance)
	}
	if self.Player.Username != "Alice" {
		t.Fatalf("expected saved username, got %q", self.Player.Username)
	}
}

func TestHubReplacementIgnoresSavedPositionLoader(t *testing.T) {
	loader := SavedPlayerLoader(func(userID int64) (SavedPlayerLoad, bool) {
		return SavedPlayerLoad{
			Lat:         31.99,
			Lng:         121.99,
			HasPosition: true,
			Appearance:  game.Appearance{Color: "#000000", Shape: game.ShapeSquare},
		}, true
	})

	hub, simulations, broadcasts, _ := newTestHubWithLoader(loader, nil)
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient(1001, 8)
	replacement := NewTestClient(1001, 8)
	hub.Register(old)
	mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, old)
	broadcasts <- time.Now()
	moved := mustReceiveReplicationUpdate(t, old)
	if moved.SelfPosition == nil {
		t.Fatalf("expected self position replication: %+v", moved)
	}
	movedLng := moved.SelfPosition.Lng

	hub.Register(replacement)
	self, _ := mustReceiveInitialization(t, replacement)
	if self.Player.Lng != movedLng {
		t.Fatalf("replacement reloaded stale saved position: got %v want %v", self.Player.Lng, movedLng)
	}
}

func TestHubReplacementRetainsInMemoryPosition(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient(1001, 8)
	replacement := NewTestClient(1001, 8)
	hub.Register(old)
	mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, old)
	broadcasts <- time.Now()
	moved := mustReceiveReplicationUpdate(t, old)
	if moved.SelfPosition == nil {
		t.Fatalf("unexpected movement replication: %+v", moved)
	}
	movedLng := moved.SelfPosition.Lng
	if movedLng <= testWorldConfig().SpawnLng {
		t.Fatalf("expected player to move right from spawn, got %+v", moved.SelfPosition)
	}

	hub.Register(replacement)
	self, _ := mustReceiveInitialization(t, replacement)
	if self.Player.Lng != movedLng {
		t.Fatalf("replacement reset position: got %v want %v", self.Player.Lng, movedLng)
	}

	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	assertNoMessage(t, replacement)
	broadcasts <- time.Now()
	replacementUpdate := mustReceiveReplicationUpdate(t, replacement)
	if replacementUpdate.SelfPosition == nil || replacementUpdate.SelfPosition.Lng >= movedLng {
		t.Fatalf("replacement connection did not control player: %+v", replacementUpdate.SelfPosition)
	}

	hub.Unregister(old)
	broadcasts <- time.Now()
	assertNoMessage(t, replacement)
}

func TestHubReplacementSurvivesObsoleteUnregister(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient(1001, 8)
	replacement := NewTestClient(1001, 8)
	hub.Register(old)
	mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	hub.Register(replacement)
	mustReceiveInitialization(t, replacement)

	hub.Unregister(old)
	broadcasts <- time.Now()
	assertNoMessage(t, replacement)

	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, replacement)
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, replacement)
	if len(update.LeftPlayerIDs) != 0 {
		t.Fatalf("replacement removed: %+v", update.LeftPlayerIDs)
	}
}

func TestHubRejectsInputFromReplacedConnection(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient(1001, 8)
	replacement := NewTestClient(1001, 8)
	hub.Register(old)
	mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	hub.ApplyInput(old, game.InputState{Sequence: 1, Right: true})
	hub.Register(replacement)
	mustReceiveInitialization(t, replacement)
	hub.ApplyInput(old, game.InputState{Sequence: 2, Right: true})
	hub.ApplyInput(replacement, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, replacement)
	if update.SelfPosition == nil {
		t.Fatalf("unexpected replacement replication: %+v", update)
	}
	if update.SelfPosition.Lng >= testWorldConfig().SpawnLng {
		t.Fatalf("stale old connection controlled player: %+v", update.SelfPosition)
	}
	if len(update.LeftPlayerIDs) != 0 {
		t.Fatalf("replacement must not emit removal: %+v", update.LeftPlayerIDs)
	}

	hub.Unregister(old)
	broadcasts <- time.Now()
	assertNoMessage(t, replacement)
}

func TestHubDropsSlowClient(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	slow := NewTestClient(1004, 0)
	fast := NewTestClient(1005, 8)
	hub.Register(slow)
	hub.Register(fast)
	mustReceiveInitialization(t, fast)

	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("expected slow client to close")
	}

	broadcasts <- time.Now()
	assertNoMessage(t, fast)
}

func TestHubMethodsReturnAfterStop(t *testing.T) {
	hub, _, _, _ := newTestHub()
	go hub.Run()
	hub.Stop()

	client := NewTestClient(1001, 1)
	if hub.Register(client) {
		t.Fatal("register should fail after stop")
	}
	if hub.ApplyInput(client, game.InputState{Sequence: 1, Up: true}) {
		t.Fatal("input should fail after stop")
	}
	if hub.UpdateAppearance(1001, game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}) {
		t.Fatal("appearance update should fail after stop")
	}
	hub.Unregister(client)
}

func TestHubUpdateAppearanceBroadcastsToAllClients(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance(1001, updated) {
		t.Fatal("appearance update failed")
	}

	broadcasts <- time.Now()
	aliceUpdate := mustReceiveReplicationUpdate(t, alice)
	bobUpdate := mustReceiveReplicationUpdate(t, bob)
	if len(aliceUpdate.Appearances) != 1 || aliceUpdate.Appearances[0].PlayerID != 1001 || aliceUpdate.Appearances[0].Appearance != updated {
		t.Fatalf("unexpected alice appearance replication: %+v", aliceUpdate)
	}
	if len(bobUpdate.Appearances) != 1 || bobUpdate.Appearances[0] != aliceUpdate.Appearances[0] {
		t.Fatalf("clients received different appearance replication: %+v %+v", aliceUpdate, bobUpdate)
	}

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
	assertNoMessage(t, bob)
}

func TestHubUpdateAppearanceInvisibleNeighborSuppressed(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {100, 0},
		1003: {900, 0},
	})
	hub, _, broadcasts, _ := newTestHubWithLoader(loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	carol := NewTestClient(1003, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	hub.Register(carol)
	mustReceiveInitialization(t, carol)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)
	assertNoMessage(t, carol)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance(1001, updated) {
		t.Fatal("appearance update failed")
	}

	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	mustReceiveReplicationUpdate(t, bob)
	assertNoMessage(t, carol)
}

func TestHubUpdateAppearanceCollapsesToFinalValue(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	first := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	second := game.Appearance{Color: "#00aa66", Shape: game.ShapeTriangle}
	if !hub.UpdateAppearance(1001, first) {
		t.Fatal("first appearance update failed")
	}
	if !hub.UpdateAppearance(1001, second) {
		t.Fatal("second appearance update failed")
	}

	broadcasts <- time.Now()
	aliceUpdate := mustReceiveReplicationUpdate(t, alice)
	bobUpdate := mustReceiveReplicationUpdate(t, bob)
	if len(aliceUpdate.Appearances) != 1 || aliceUpdate.Appearances[0].Appearance != second {
		t.Fatalf("expected final appearance for alice, got %+v", aliceUpdate)
	}
	if len(bobUpdate.Appearances) != 1 || bobUpdate.Appearances[0].Appearance != second {
		t.Fatalf("expected final appearance for bob, got %+v", bobUpdate)
	}
}

func TestHubUpdateAppearanceLeftPrecedence(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {100, 0},
	})
	hub, _, broadcasts, _ := newTestHubWithLoader(loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance(1001, updated) {
		t.Fatal("appearance update failed")
	}
	hub.Unregister(alice)

	broadcasts <- time.Now()
	update := mustReceiveReplicationUpdate(t, bob)
	if len(update.LeftPlayerIDs) != 1 || update.LeftPlayerIDs[0] != 1001 {
		t.Fatalf("expected alice left, got %+v", update)
	}
	if len(update.Appearances) != 0 {
		t.Fatalf("left should suppress appearance, got %+v", update)
	}
}

func TestHubUpdateAppearanceEnteredIncludesFinalAppearance(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {100, 0},
	})
	hub, _, broadcasts, _ := newTestHubWithLoader(loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	hub.Register(bob)
	mustReceiveInitialization(t, bob)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance(1002, updated) {
		t.Fatal("appearance update failed")
	}

	broadcasts <- time.Now()
	joined := mustReceiveReplicationUpdate(t, alice)
	if len(joined.Entered) != 1 || joined.Entered[0].ID != 1002 {
		t.Fatalf("expected bob entered, got %+v", joined)
	}
	if joined.Entered[0].Appearance != updated {
		t.Fatalf("entered should carry final appearance, got %+v", joined.Entered[0].Appearance)
	}
	if len(joined.Appearances) != 0 {
		t.Fatalf("entered should exclude duplicate appearance, got %+v", joined)
	}
}

func TestHubUpdateAppearanceReturnsBeforeReplicationTick(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance(1001, updated) {
		t.Fatal("appearance update failed")
	}
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	update := mustReceiveReplicationUpdate(t, alice)
	if len(update.Appearances) != 1 || update.Appearances[0].Appearance != updated {
		t.Fatalf("unexpected appearance replication: %+v", update)
	}
}

func TestHubUpdateAppearanceUnchangedDoesNotBroadcast(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	self, _ := mustReceiveInitialization(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	if !hub.UpdateAppearance(1001, self.Player.Appearance) {
		t.Fatal("unchanged appearance update failed")
	}
	assertNoMessage(t, alice)
}

func TestHubUpdateAppearanceOfflineUserSucceedsWithoutBroadcast(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeTriangle}
	if !hub.UpdateAppearance(1006, updated) {
		t.Fatal("offline appearance update failed")
	}
	assertNoMessage(t, alice)
}

func TestHubReplacementRetainsInMemoryAppearance(t *testing.T) {
	loader := SavedPlayerLoader(func(userID int64) (SavedPlayerLoad, bool) {
		return SavedPlayerLoad{
			Lat:         31.99,
			Lng:         121.99,
			HasPosition: true,
			Appearance:  game.Appearance{Color: "#000000", Shape: game.ShapeSquare},
		}, true
	})

	hub, _, broadcasts, _ := newTestHubWithLoader(loader, nil)
	go hub.Run()
	defer hub.Stop()

	old := NewTestClient(1001, 8)
	replacement := NewTestClient(1001, 8)
	hub.Register(old)
	initial, _ := mustReceiveInitialization(t, old)
	broadcasts <- time.Now()
	assertNoMessage(t, old)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance(1001, updated) {
		t.Fatal("appearance update failed")
	}
	broadcasts <- time.Now()
	appearanceUpdate := mustReceiveReplicationUpdate(t, old)
	if len(appearanceUpdate.Appearances) != 1 || appearanceUpdate.Appearances[0].Appearance != updated {
		t.Fatalf("unexpected appearance replication: %+v", appearanceUpdate)
	}

	hub.Register(replacement)
	self, _ := mustReceiveInitialization(t, replacement)
	if self.Player.Appearance != updated {
		t.Fatalf("replacement reloaded stale appearance: got %+v want %+v", self.Player.Appearance, updated)
	}
	if self.Player.Appearance == initial.Player.Appearance {
		t.Fatalf("expected appearance to change before replacement: %+v", initial.Player.Appearance)
	}
}

func TestHubDisconnectUserRemovesPlayer(t *testing.T) {
	hub, _, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.DisconnectUser(1001)

	broadcasts <- time.Now()
	update := mustReceiveReplicationUpdate(t, bob)
	if len(update.LeftPlayerIDs) != 1 || update.LeftPlayerIDs[0] != 1001 {
		t.Fatalf("expected alice removed, got %+v", update)
	}

	select {
	case <-alice.done:
	case <-time.After(time.Second):
		t.Fatal("expected alice client to close after disconnect")
	}
}

func TestHubFirstConnectionSeesOnlyNearbyPlayers(t *testing.T) {
	hub, _, _, _ := newTestHubWithLoader(distantPlayerLoader(), nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	_, aliceVisible := mustReceiveInitialization(t, alice)
	if len(aliceVisible.Players) != 0 {
		t.Fatalf("alice should see no players alone, got %+v", aliceVisible.Players)
	}

	hub.Register(bob)
	_, bobVisible := mustReceiveInitialization(t, bob)
	if len(bobVisible.Players) != 0 {
		t.Fatalf("bob should not see distant alice, got %+v", bobVisible.Players)
	}
}

func TestHubNearbyNeighborReceivesEnteredOnNextTick(t *testing.T) {
	hub, _, broadcasts, _ := newTestHubWithLoader(nearbyPlayerLoader(), nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	hub.Register(bob)
	_, bobVisible := mustReceiveInitialization(t, bob)
	if len(bobVisible.Players) != 1 || bobVisible.Players[0].ID != 1001 {
		t.Fatalf("bob should see alice in snapshot, got %+v", bobVisible.Players)
	}

	broadcasts <- time.Now()
	joined := mustReceiveReplicationUpdate(t, alice)
	if len(joined.Entered) != 1 || joined.Entered[0].ID != 1002 {
		t.Fatalf("expected bob entered for alice, got %+v", joined)
	}
	assertNoMessage(t, bob)
}

func TestHubDistantPlayerDoesNotReceiveNeighborReplication(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHubWithLoader(distantPlayerLoader(), nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)
	assertNoMessage(t, bob)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, alice)
	if update.SelfPosition == nil {
		t.Fatalf("expected alice self movement, got %+v", update)
	}
	assertNoMessage(t, bob)
}

func TestHubReplacementRetainsHysteresisVisibility(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), hysteresisPlayerLoader(), nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	_, bobVisible := mustReceiveInitialization(t, bob)
	if len(bobVisible.Players) != 1 || bobVisible.Players[0].ID != 1001 {
		t.Fatalf("bob should see alice, got %+v", bobVisible.Players)
	}

	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()
	moved := mustReceiveReplicationUpdate(t, alice)
	if len(moved.Positions) != 1 || moved.Positions[0].ID != 1002 {
		t.Fatalf("expected bob position for alice, got %+v", moved)
	}

	replacement := NewTestClient(1002, 8)
	hub.Register(replacement)
	_, replacementVisible := mustReceiveInitialization(t, replacement)
	if len(replacementVisible.Players) != 1 || replacementVisible.Players[0].ID != 1001 {
		t.Fatalf("replacement should retain alice in hysteresis band, got %+v", replacementVisible.Players)
	}

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
}

func TestHubTrueOfflineReconnectRebuildsNearbyRelationshipsOnly(t *testing.T) {
	hub, _, broadcasts, _ := newTestHubWithLoader(nearbyPlayerLoader(), nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.Unregister(bob)
	broadcasts <- time.Now()
	left := mustReceiveReplicationUpdate(t, alice)
	if len(left.LeftPlayerIDs) != 1 || left.LeftPlayerIDs[0] != 1002 {
		t.Fatalf("expected bob left for alice, got %+v", left)
	}

	bobAgain := NewTestClient(1002, 8)
	hub.Register(bobAgain)
	_, bobVisible := mustReceiveInitialization(t, bobAgain)
	if len(bobVisible.Players) != 1 || bobVisible.Players[0].ID != 1001 {
		t.Fatalf("reconnect should rebuild nearby visibility, got %+v", bobVisible.Players)
	}

	broadcasts <- time.Now()
	joined := mustReceiveReplicationUpdate(t, alice)
	if len(joined.Entered) != 1 || joined.Entered[0].ID != 1002 {
		t.Fatalf("expected bob entered for alice after reconnect, got %+v", joined)
	}
}

func TestHubTrueOfflineReconnectSkipsDistantPlayers(t *testing.T) {
	hub, _, broadcasts, _ := newTestHubWithLoader(distantPlayerLoader(), nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	hub.Unregister(bob)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	bobAgain := NewTestClient(1002, 8)
	hub.Register(bobAgain)
	_, bobVisible := mustReceiveInitialization(t, bobAgain)
	if len(bobVisible.Players) != 0 {
		t.Fatalf("distant reconnect should see no players, got %+v", bobVisible.Players)
	}

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
}

func TestHubReplacementClearsPendingLeft(t *testing.T) {
	hub, _, broadcasts, _ := newTestHubWithLoader(nearbyPlayerLoader(), nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.Unregister(bob)
	replacement := NewTestClient(1002, 8)
	hub.Register(replacement)
	mustReceiveInitialization(t, replacement)

	broadcasts <- time.Now()
	assertNoMessage(t, replacement)
}

func TestHubTwoSimulationTicksOneReplication(t *testing.T) {
	hub, simulations, broadcasts, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	hub.ApplyInput(alice, game.InputState{Sequence: 2, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)

	broadcasts <- time.Now()
	update := mustReceiveReplicationUpdate(t, alice)
	if update.SelfPosition == nil {
		t.Fatalf("expected one self position after two simulation ticks: %+v", update)
	}
	assertNoMessage(t, alice)
}

func TestHubMovementTriggersStationaryPeerEntered(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {700, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	_, bobVisible := mustReceiveInitialization(t, bob)
	if len(bobVisible.Players) != 0 {
		t.Fatalf("bob should start with no visible players, got %+v", bobVisible.Players)
	}
	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	hub.ApplyInput(bob, game.InputState{Sequence: 2, Left: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	joined := mustReceiveReplicationUpdate(t, alice)
	if len(joined.Entered) != 1 || joined.Entered[0].ID != 1002 {
		t.Fatalf("expected bob entered for alice, got %+v", joined)
	}
	if len(joined.Positions) != 0 {
		t.Fatalf("entered player should not also appear in positions: %+v", joined)
	}
	bobUpdate := mustReceiveReplicationUpdate(t, bob)
	if bobUpdate.SelfPosition == nil {
		t.Fatalf("expected bob self movement, got %+v", bobUpdate)
	}
}

func TestHubMovementExitQueuesLeft(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {400, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	hub.ApplyInput(bob, game.InputState{Sequence: 2, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	left := mustReceiveReplicationUpdate(t, alice)
	if len(left.LeftPlayerIDs) != 1 || left.LeftPlayerIDs[0] != 1002 {
		t.Fatalf("expected bob left for alice, got %+v", left)
	}
	if len(left.Positions) != 0 {
		t.Fatalf("left player should not appear in positions: %+v", left)
	}
}

func TestHubVisibleMovementSendsPositionOnly(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {100, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, alice)
	if len(update.Positions) != 1 || update.Positions[0].ID != 1002 {
		t.Fatalf("expected bob position for alice, got %+v", update)
	}
	if len(update.Entered) != 0 || len(update.LeftPlayerIDs) != 0 {
		t.Fatalf("expected position-only update, got %+v", update)
	}
}

func TestHubStaticDistantClientReceivesNoUpdate(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {100, 0},
		1003: {900, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	carol := NewTestClient(1003, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	hub.Register(carol)
	mustReceiveInitialization(t, carol)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)
	assertNoMessage(t, carol)

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, carol)
}

func TestHubHysteresisMovementRetainsVisibility(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {400, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, alice)
	if len(update.Positions) != 1 || update.Positions[0].ID != 1002 {
		t.Fatalf("expected bob position in hysteresis band, got %+v", update)
	}
	if len(update.LeftPlayerIDs) != 0 {
		t.Fatalf("hysteresis move should not emit left: %+v", update)
	}
}

func TestHubOneMessagePerClientPerTick(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {100, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	hub.ApplyInput(bob, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, alice)
	mustReceiveReplicationUpdate(t, bob)
	assertNoMessage(t, bob)
}

func TestHubSlowClientRemovalPreservesAOI(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {100, 0},
		1004:  {900, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	slow := NewTestClient(1004, 0)
	hub.Register(slow)

	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("expected slow client to close")
	}

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, alice)
	if len(update.Positions) != 1 || update.Positions[0].ID != 1002 {
		t.Fatalf("expected bob position after slow client removal, got %+v", update)
	}
}

// 多移动者稳定可见 — 观察者收到每个移动者的位置
func TestHubMultipleStableMoversUpdateOneObserver(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002: {100, 0},
		1003: {0, 100},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	carol := NewTestClient(1003, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	hub.Register(carol)
	mustReceiveInitialization(t, carol)

	broadcasts <- time.Now()
	entered := mustReceiveReplicationUpdate(t, alice)
	if len(entered.Entered) != 2 {
		t.Fatalf("expected 2 entered for alice, got %+v", entered)
	}
	// bob 也看到 carol 进入
	mustReceiveReplicationUpdate(t, bob)
	// carol 也看到 bob 进入
	mustReceiveReplicationUpdate(t, carol)

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	hub.ApplyInput(carol, game.InputState{Sequence: 1, Up: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, alice)
	if len(update.Positions) != 2 {
		t.Fatalf("expected 2 positions for alice, got %+v", update)
	}
	ids := map[int64]bool{}
	for _, p := range update.Positions {
		ids[p.ID] = true
	}
	if !ids[1002] || !ids[1003] {
		t.Fatalf("expected positions for 1002 and 1003, got %+v", update.Positions)
	}
	if len(update.Entered) != 0 || len(update.LeftPlayerIDs) != 0 {
		t.Fatalf("expected position-only update, got %+v", update)
	}
}

// 同广播多进入 — 两个玩家在同一 tick 进入观察者视野
func TestHubSameBroadcastMultipleEntry(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002: {550, 0},
		1003: {0, 550},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	carol := NewTestClient(1003, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	hub.Register(carol)
	mustReceiveInitialization(t, carol)

	broadcasts <- time.Now()
	assertNoMessage(t, alice)

	// 每个玩家两帧，进入 alice 的 500m 范围
	hub.ApplyInput(bob, game.InputState{Sequence: 1, Left: true})
	hub.ApplyInput(carol, game.InputState{Sequence: 1, Down: true})
	simulations <- time.Now()
	hub.ApplyInput(bob, game.InputState{Sequence: 2, Left: true})
	hub.ApplyInput(carol, game.InputState{Sequence: 2, Down: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, alice)
	if len(update.Entered) != 2 {
		t.Fatalf("expected 2 entered for alice, got %+v", update)
	}
	enteredIDs := map[int64]bool{}
	for _, e := range update.Entered {
		enteredIDs[e.ID] = true
	}
	if !enteredIDs[1002] || !enteredIDs[1003] {
		t.Fatalf("expected entered for 1002 and 1003, got %+v", update.Entered)
	}
	// 进入的玩家不应同时出现在 positions 中
	if len(update.Positions) != 0 {
		t.Fatalf("entered players should not appear in positions: %+v", update)
	}
}

// 同广播多离开 — 两个玩家在同一 tick 离开观察者视野
func TestHubSameBroadcastMultipleLeave(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002: {350, 0},
		1003: {0, 350},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	carol := NewTestClient(1003, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	hub.Register(carol)
	mustReceiveInitialization(t, carol)

	broadcasts <- time.Now()
	entered := mustReceiveReplicationUpdate(t, alice)
	if len(entered.Entered) != 2 {
		t.Fatalf("expected 2 entered for alice, got %+v", entered)
	}
	// bob 和 carol 也互见
	mustReceiveReplicationUpdate(t, bob)
	mustReceiveReplicationUpdate(t, carol)

	// 两个移动者远离 alice，各三帧，确保超出 600m 离开范围
	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	hub.ApplyInput(carol, game.InputState{Sequence: 1, Up: true})
	simulations <- time.Now()
	hub.ApplyInput(bob, game.InputState{Sequence: 2, Right: true})
	hub.ApplyInput(carol, game.InputState{Sequence: 2, Up: true})
	simulations <- time.Now()
	hub.ApplyInput(bob, game.InputState{Sequence: 3, Right: true})
	hub.ApplyInput(carol, game.InputState{Sequence: 3, Up: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()

	update := mustReceiveReplicationUpdate(t, alice)
	if len(update.LeftPlayerIDs) != 2 {
		t.Fatalf("expected 2 left for alice, got %+v", update)
	}
	// 离开的玩家不应出现在 positions 中
	if len(update.Positions) != 0 {
		t.Fatalf("left players should not appear in positions: %+v", update)
	}
}

// 移动者只收到 SelfPosition，不收到自己的 positions/entered/left
func TestHubSelfPositionNotDuplicatedInOtherFields(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002: {100, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)

	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice) // bob entered
	assertNoMessage(t, bob)

	// 两个人都移动
	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	aliceUpdate := mustReceiveReplicationUpdate(t, alice)
	if aliceUpdate.SelfPosition == nil {
		t.Fatalf("expected self position for alice, got %+v", aliceUpdate)
	}
	for _, p := range aliceUpdate.Positions {
		if p.ID == 1001 {
			t.Fatalf("self should not appear in positions: %+v", aliceUpdate)
		}
	}
	for _, e := range aliceUpdate.Entered {
		if e.ID == 1001 {
			t.Fatalf("self should not appear in entered: %+v", aliceUpdate)
		}
	}
	for _, id := range aliceUpdate.LeftPlayerIDs {
		if id == 1001 {
			t.Fatalf("self should not appear in left: %+v", aliceUpdate)
		}
	}
}

// selectiveFailClient 用于测试队列满时的移除场景
type selectiveFailClient struct {
	*testClient
	fail bool
}

func (c *selectiveFailClient) Send(data []byte) bool {
	if c.fail {
		return false
	}
	return c.testClient.Send(data)
}

// 队列满移除时，其他累积接收者仍收到有效更新
func TestHubQueueFullRemovalPreservesOtherRecipients(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002: {100, 0},
		1003: {0, 100},
		1004: {100, 100},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	carol := NewTestClient(1003, 8)
	failClient := &selectiveFailClient{testClient: NewTestClient(1004, 8)}

	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	hub.Register(carol)
	mustReceiveInitialization(t, carol)
	hub.Register(failClient)
	mustReceiveInitialization(t, failClient.testClient)

	broadcasts <- time.Now()
	// 第一轮广播 — 所有人收到其他玩家的进入消息
	mustReceiveReplicationUpdate(t, alice)
	mustReceiveReplicationUpdate(t, bob)
	mustReceiveReplicationUpdate(t, carol)
	mustReceiveReplicationUpdate(t, failClient.testClient)

	// 让 failClient 在复制阶段 Send 失败
	failClient.fail = true

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	hub.ApplyInput(bob, game.InputState{Sequence: 2, Right: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	// alice 和 carol 仍应收到 bob 的位置更新
	aliceUpdate := mustReceiveReplicationUpdate(t, alice)
	if len(aliceUpdate.Positions) != 1 || aliceUpdate.Positions[0].ID != 1002 {
		t.Fatalf("expected bob position for alice, got %+v", aliceUpdate)
	}
	carolUpdate := mustReceiveReplicationUpdate(t, carol)
	if len(carolUpdate.Positions) != 1 || carolUpdate.Positions[0].ID != 1002 {
		t.Fatalf("expected bob position for carol, got %+v", carolUpdate)
	}

	// failClient 应被移除
	select {
	case <-failClient.done:
	case <-time.After(time.Second):
		t.Fatal("expected fail client to be removed")
	}
}

// 移动触发的进入方向性 — 观察者收到移动者的 entered，移动者不收到观察者的 entered
func TestHubMovementEntryDirectionality(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002: {550, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)

	broadcasts <- time.Now()
	assertNoMessage(t, alice)
	assertNoMessage(t, bob)

	// bob 向 alice 移动，进入 alice 的 500m 范围
	hub.ApplyInput(bob, game.InputState{Sequence: 1, Left: true})
	simulations <- time.Now()
	hub.ApplyInput(bob, game.InputState{Sequence: 2, Left: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	// alice（观察者）收到 bob 的 entered
	aliceUpdate := mustReceiveReplicationUpdate(t, alice)
	if len(aliceUpdate.Entered) != 1 || aliceUpdate.Entered[0].ID != 1002 {
		t.Fatalf("expected bob entered for alice, got %+v", aliceUpdate)
	}

	// bob（移动者）不收到 alice 的 entered
	bobUpdate := mustReceiveReplicationUpdate(t, bob)
	if bobUpdate.SelfPosition == nil {
		t.Fatalf("expected self position for bob, got %+v", bobUpdate)
	}
	for _, e := range bobUpdate.Entered {
		if e.ID == 1001 {
			t.Fatalf("mover should not receive observer as entered: %+v", bobUpdate)
		}
	}
}

// 移动触发的离开方向性 — 观察者收到移动者的 left，移动者不收到观察者的 left
func TestHubMovementLeaveDirectionality(t *testing.T) {
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002: {450, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfig(fastTestWorldConfig(), loader, nil)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)

	broadcasts <- time.Now()
	entered := mustReceiveReplicationUpdate(t, alice)
	if len(entered.Entered) != 1 || entered.Entered[0].ID != 1002 {
		t.Fatalf("expected bob entered for alice, got %+v", entered)
	}
	assertNoMessage(t, bob)

	// bob 远离 alice，超出 600m 离开范围
	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	hub.ApplyInput(bob, game.InputState{Sequence: 2, Right: true})
	simulations <- time.Now()
	broadcasts <- time.Now()

	// alice（观察者）收到 bob 的 left
	aliceUpdate := mustReceiveReplicationUpdate(t, alice)
	if len(aliceUpdate.LeftPlayerIDs) != 1 || aliceUpdate.LeftPlayerIDs[0] != 1002 {
		t.Fatalf("expected bob left for alice, got %+v", aliceUpdate)
	}

	// bob（移动者）不收到 alice 的 left
	bobUpdate := mustReceiveReplicationUpdate(t, bob)
	if bobUpdate.SelfPosition == nil {
		t.Fatalf("expected self position for bob, got %+v", bobUpdate)
	}
	for _, id := range bobUpdate.LeftPlayerIDs {
		if id == 1001 {
			t.Fatalf("mover should not receive observer as left: %+v", bobUpdate)
		}
	}
}

func TestHubLogsAOIStats(t *testing.T) {
	var logOutput syncBuffer
	originalWriter := log.Writer()
	log.SetOutput(&logOutput)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	statsTick := make(chan time.Time, 8)
	loader := fixedPositionsLoader(map[int64][2]float64{
		1001: {0, 0},
		1002:   {100, 0},
	})
	hub, simulations, broadcasts, _ := newTestHubWithConfigAndStats(fastTestWorldConfig(), loader, nil, statsTick)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	bob := NewTestClient(1002, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)
	hub.Register(bob)
	mustReceiveInitialization(t, bob)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)
	assertNoMessage(t, bob)

	hub.ApplyInput(bob, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	assertNoMessage(t, alice)
	broadcasts <- time.Now()
	mustReceiveReplicationUpdate(t, alice)

	statsTick <- time.Now()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logOutput.String(), "replication_messages=3") {
			break
		}
		time.Sleep(time.Millisecond)
	}

	output := logOutput.String()
	for _, want := range []string{
		"moved_players=1",
		"aoi_candidates=1",
		"aoi_distance_checks=3",
		"aoi_entered=1",
		"replication_messages=3",
		"replication_recipients=3",
		"replication_bytes=353",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected stats log to contain %q, got:\n%s", want, output)
		}
	}
}

func TestHubDisconnectUserUnknownIDIsNoop(t *testing.T) {
	hub, _, _, _ := newTestHub()
	go hub.Run()
	defer hub.Stop()

	hub.DisconnectUser(9999)
}

func TestHubDisconnectUserDoesNotBlockAfterStop(t *testing.T) {
	hub, _, _, _ := newTestHub()
	go hub.Run()
	hub.Stop()

	done := make(chan struct{})
	go func() {
		hub.DisconnectUser(1001)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DisconnectUser blocked after stop")
	}
}

func newTestHub() (*Hub, chan time.Time, chan time.Time, chan time.Time) {
	return newTestHubWithLoader(nil, nil)
}

func newTestHubWithLoader(loader SavedPlayerLoader, persister PositionPersister) (*Hub, chan time.Time, chan time.Time, chan time.Time) {
	return newTestHubWithConfig(testWorldConfig(), loader, persister)
}

func newTestHubWithConfig(config game.Config, loader SavedPlayerLoader, persister PositionPersister) (*Hub, chan time.Time, chan time.Time, chan time.Time) {
	return newTestHubWithConfigAndStats(config, loader, persister, nil)
}

func newTestHubWithConfigAndStats(config game.Config, loader SavedPlayerLoader, persister PositionPersister, statsTick chan time.Time) (*Hub, chan time.Time, chan time.Time, chan time.Time) {
	simulations := make(chan time.Time, 8)
	broadcasts := make(chan time.Time, 8)
	persistence := make(chan time.Time, 8)
	world := game.NewWorld(config)
	hub := newHub(world, loader, persister, nil, nil, simulations, broadcasts, persistence, statsTick, func() {})
	return hub, simulations, broadcasts, persistence
}

func testWorldConfig() game.Config {
	return game.Config{
		SpawnLat:             31.2304,
		SpawnLng:             121.4737,
		SpeedMetersPerSecond: 12,
	}
}

func fastTestWorldConfig() game.Config {
	config := testWorldConfig()
	config.SpeedMetersPerSecond = 3000
	return config
}

func testAOIConfig() game.AOIConfig {
	return game.AOIConfigFromWorld(testWorldConfig())
}

func localLatLng(localX, localY float64) (float64, float64) {
	return testAOIConfig().LocalToLatLng(localX, localY)
}

func nearbyPlayerLoader() SavedPlayerLoader {
	return func(userID int64) (SavedPlayerLoad, bool) {
		switch userID {
		case 1001:
			lat, lng := localLatLng(0, 0)
			return SavedPlayerLoad{Username: "alice", Lat: lat, Lng: lng, HasPosition: true}, true
		case 1002:
			lat, lng := localLatLng(100, 0)
			return SavedPlayerLoad{Username: "bob", Lat: lat, Lng: lng, HasPosition: true}, true
		}
		return SavedPlayerLoad{}, false
	}
}

func distantPlayerLoader() SavedPlayerLoader {
	return func(userID int64) (SavedPlayerLoad, bool) {
		switch userID {
		case 1001:
			lat, lng := localLatLng(0, 0)
			return SavedPlayerLoad{Username: "alice", Lat: lat, Lng: lng, HasPosition: true}, true
		case 1002:
			lat, lng := localLatLng(700, 0)
			return SavedPlayerLoad{Username: "bob", Lat: lat, Lng: lng, HasPosition: true}, true
		}
		return SavedPlayerLoad{}, false
	}
}

func hysteresisPlayerLoader() SavedPlayerLoader {
	return func(userID int64) (SavedPlayerLoad, bool) {
		switch userID {
		case 1001:
			lat, lng := localLatLng(0, 0)
			return SavedPlayerLoad{Username: "alice", Lat: lat, Lng: lng, HasPosition: true}, true
		case 1002:
			lat, lng := localLatLng(400, 0)
			return SavedPlayerLoad{Username: "bob", Lat: lat, Lng: lng, HasPosition: true}, true
		}
		return SavedPlayerLoad{}, false
	}
}

func fixedPositionsLoader(positions map[int64][2]float64) SavedPlayerLoader {
	return func(userID int64) (SavedPlayerLoad, bool) {
		coords, ok := positions[userID]
		if !ok {
			return SavedPlayerLoad{}, false
		}
		lat, lng := localLatLng(coords[0], coords[1])
		return SavedPlayerLoad{
			Username:    fmt.Sprintf("%d", userID),
			Lat:         lat,
			Lng:         lng,
			HasPosition: true,
		}, true
	}
}

type testClient struct {
	id   int64
	send chan []byte
	done chan struct{}
}

func NewTestClient(id int64, buffer int) *testClient {
	return &testClient{
		id:   id,
		send: make(chan []byte, buffer),
		done: make(chan struct{}),
	}
}

func (c *testClient) ID() int64 {
	return c.id
}

func (c *testClient) Username() string {
	return fmt.Sprintf("%d", c.id)
}

func (c *testClient) Send(data []byte) bool {
	select {
	case c.send <- data:
		return true
	default:
		return false
	}
}

func (c *testClient) CloseSend() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

func (c *testClient) drainAll() {
	for {
		select {
		case <-c.send:
		default:
			return
		}
	}
}

func mustReceiveInitialization(t *testing.T, client *testClient) (SelfStateMessage, VisibleEntitiesSnapshotMessage) {
	t.Helper()
	var self SelfStateMessage
	if err := json.Unmarshal(mustReceiveData(t, client), &self); err != nil {
		t.Fatalf("decode self state failed: %v", err)
	}
	if self.Type != MessageTypeSelfState {
		t.Fatalf("expected self state, got %q", self.Type)
	}

	var visible VisibleEntitiesSnapshotMessage
	if err := json.Unmarshal(mustReceiveData(t, client), &visible); err != nil {
		t.Fatalf("decode visible entities snapshot failed: %v", err)
	}
	if visible.Type != MessageTypeVisibleEntitiesSnapshot {
		t.Fatalf("expected visible entities snapshot, got %q", visible.Type)
	}

	// 消费收集品初始化消息（collectible_regions + visible_collectibles_snapshot）
	mustReceiveData(t, client) // collectible_regions
	mustReceiveData(t, client) // visible_collectibles_snapshot
	return self, visible
}

func mustReceiveReplicationUpdate(t *testing.T, client *testClient) ReplicationUpdateMessage {
	t.Helper()
	data := mustReceiveData(t, client)
	var message ReplicationUpdateMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode replication update failed: %v", err)
	}
	if message.Type != MessageTypeReplicationUpdate {
		t.Fatalf("expected replication update, got %q", message.Type)
	}
	return message
}

func mustReceiveData(t *testing.T, client *testClient) []byte {
	t.Helper()
	select {
	case data := <-client.send:
		return data
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

// syncBuffer is a bytes.Buffer safe for concurrent reads and writes,
// needed when the Hub goroutine logs while the test goroutine polls the output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func assertNoMessage(t *testing.T, client *testClient) {
	t.Helper()
	select {
	case data := <-client.send:
		t.Fatalf("unexpected message: %s", data)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestHubSnapshotNilBeforeFirstStatsTick(t *testing.T) {
	statsTick := make(chan time.Time, 8)
	hub, _, _, _ := newTestHubWithConfigAndStats(testWorldConfig(), nil, nil, statsTick)
	go hub.Run()
	defer hub.Stop()

	if hub.Snapshot() != nil {
		t.Fatal("expected nil snapshot before first stats tick")
	}
}

func TestHubSnapshotConnectedClientsCount(t *testing.T) {
	statsTick := make(chan time.Time, 8)
	hub, _, _, _ := newTestHubWithConfigAndStats(testWorldConfig(), nil, nil, statsTick)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1, 8)
	bob := NewTestClient(2, 8)
	hub.Register(alice)
	hub.Register(bob)
	mustReceiveInitialization(t, alice)
	mustReceiveInitialization(t, bob)

	statsTick <- time.Now()
	// Poll until snapshot is published.
	deadline := time.Now().Add(time.Second)
	for hub.Snapshot() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	snap := hub.Snapshot()
	if snap == nil {
		t.Fatal("snapshot is nil after stats tick")
	}
	if snap.ConnectedClients != 2 {
		t.Errorf("ConnectedClients=%d want 2", snap.ConnectedClients)
	}
}

func TestHubSnapshotReplicationCounted(t *testing.T) {
	statsTick := make(chan time.Time, 8)
	hub, simulations, broadcasts, _ := newTestHubWithConfigAndStats(testWorldConfig(), nil, nil, statsTick)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1, 8)
	bob := NewTestClient(2, 8)
	hub.Register(alice)
	hub.Register(bob)
	mustReceiveInitialization(t, alice)
	mustReceiveInitialization(t, bob)

	hub.ApplyInput(alice, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	broadcasts <- time.Now()
	// Wait for broadcast to be processed before firing stats tick.
	mustReceiveReplicationUpdate(t, alice)
	bob.drainAll()

	statsTick <- time.Now()
	deadline := time.Now().Add(time.Second)
	for hub.Snapshot() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	snap := hub.Snapshot()
	if snap == nil {
		t.Fatal("snapshot is nil after stats tick")
	}
	if snap.ReplicationMessages == 0 {
		t.Error("ReplicationMessages=0 after broadcast with movement")
	}
	if snap.ReplicationBytes == 0 {
		t.Error("ReplicationBytes=0 after broadcast with movement")
	}
	if snap.SimulationTicks == 0 {
		t.Error("SimulationTicks=0 after simulation tick")
	}
}

func TestHubSnapshotStableUntilNextTick(t *testing.T) {
	statsTick := make(chan time.Time, 8)
	hub, _, _, _ := newTestHubWithConfigAndStats(testWorldConfig(), nil, nil, statsTick)
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	statsTick <- time.Now()
	deadline := time.Now().Add(time.Second)
	for hub.Snapshot() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	first := hub.Snapshot()
	if first == nil {
		t.Fatal("first snapshot is nil")
	}
	firstSampledAt := first.SampledAt
	firstClients := first.ConnectedClients

	// Fire second stats tick without additional activity.
	statsTick <- time.Now()
	deadline = time.Now().Add(time.Second)
	for hub.Snapshot() == first && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	// Original snapshot must not have mutated.
	if first.SampledAt != firstSampledAt {
		t.Error("first snapshot mutated: SampledAt changed")
	}
	if first.ConnectedClients != firstClients {
		t.Errorf("first snapshot mutated: ConnectedClients changed from %d to %d", firstClients, first.ConnectedClients)
	}
	if hub.Snapshot() == first {
		t.Error("expected a new snapshot pointer after second stats tick")
	}
}

func TestHubSnapshotAfterStop(t *testing.T) {
	statsTick := make(chan time.Time, 8)
	hub, _, _, _ := newTestHubWithConfigAndStats(testWorldConfig(), nil, nil, statsTick)
	go hub.Run()

	alice := NewTestClient(1, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	statsTick <- time.Now()
	deadline := time.Now().Add(time.Second)
	for hub.Snapshot() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	snap := hub.Snapshot()

	hub.Stop()

	if hub.Snapshot() != snap {
		t.Error("snapshot changed after Stop")
	}
	if snap.ConnectedClients != 1 {
		t.Errorf("ConnectedClients=%d want 1", snap.ConnectedClients)
	}
}

func TestHubSnapshotDispatcherStats(t *testing.T) {
	statsTick := make(chan time.Time, 8)
	hub, _, _, _ := newTestHubWithConfigAndStats(testWorldConfig(), nil, nil, statsTick)

	// 给 Hub 装一个 dispatcher，worker 处理 job 后触发统计计数
	d := NewReplicationDispatcher(2, 8, nil)
	hub.dispatcher = d
	defer d.Stop()

	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	changes := ReplicationChanges{
		Positions: []game.PlayerPosition{{ID: 2001, Lat: 31.1, Lng: 121.1}},
	}
	cp := copyReplicationChanges(changes)
	d.Submit(replicationJob{recipientID: 1, tick: 1, client: alice, changes: cp})
	// mustReceiveReplicationUpdate 阻塞等待 worker 处理完 job
	mustReceiveReplicationUpdate(t, alice)

	statsTick <- time.Now()
	deadline := time.Now().Add(time.Second)
	for hub.Snapshot() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	snap := hub.Snapshot()
	if snap == nil {
		t.Fatal("snapshot is nil after stats tick")
	}
	if snap.Dispatcher.WorkerCount != 2 {
		t.Errorf("Dispatcher.WorkerCount=%d want 2", snap.Dispatcher.WorkerCount)
	}
	if snap.Dispatcher.Submitted == 0 {
		t.Error("Dispatcher.Submitted=0 after Submit")
	}
	if snap.Dispatcher.Encoded == 0 {
		t.Error("Dispatcher.Encoded=0 after non-empty job")
	}
}

func testRegionsForPickup() []game.CollectibleRegion {
	return []game.CollectibleRegion{
		{ID: "region-1", CenterLat: 31.2304, CenterLng: 121.4737, RadiusMeters: 5, TargetCount: 1, RespawnMin: 5 * time.Second, RespawnMax: 10 * time.Second},
	}
}

func newTestHubWithCollectibles() (*Hub, chan time.Time, chan time.Time, chan time.Time) {
	config := testWorldConfig()
	world := game.NewWorld(config)
	regions := testRegionsForPickup()
	rng := rand.New(rand.NewPCG(0, 0))
	field := game.NewCollectibleField(game.AOIConfigFromWorld(config), regions, nil, rng)
	field.Populate()

	simulations := make(chan time.Time, 8)
	broadcasts := make(chan time.Time, 8)
	persistence := make(chan time.Time, 8)
	statsTick := make(chan time.Time, 8)

	hub := newHub(world, nil, nil, field, nil, simulations, broadcasts, persistence, statsTick, func() {})
	return hub, simulations, broadcasts, persistence
}

func TestHubCollectPickupSuccess(t *testing.T) {
	hub, _, _, _ := newTestHubWithCollectibles()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	if !hub.Register(alice) {
		t.Fatal("register failed")
	}
	mustReceiveInitialization(t, alice)

	// 获取初始化后可见的收集品
	pos, _ := hub.world.PlayerPosition(1001)
	collectibles := hub.collectibleField.CollectiblesWithinRadius(pos.Lat, pos.Lng, 10)
	if len(collectibles) == 0 {
		t.Skip("10m 内无收集品，跳过拾取测试")
	}

	// 发送拾取请求
	hub.SubmitCollect(alice, collectibles[0].ID)

	// 验证收到 collect_result
	data := mustReceiveData(t, alice)
	var result CollectResultMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode collect_result failed: %v (%s)", err, string(data))
	}
	if result.Type != MessageTypeCollectResult || result.Score != 1 {
		t.Fatalf("unexpected collect_result: %+v", result)
	}
}

func TestHubCollectCooldown(t *testing.T) {
	hub, _, _, _ := newTestHubWithCollectibles()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	pos, _ := hub.world.PlayerPosition(1001)
	collectibles := hub.collectibleField.CollectiblesWithinRadius(pos.Lat, pos.Lng, 10)
	if len(collectibles) == 0 {
		t.Skip("10m 内无收集品")
	}

	// 第一次拾取
	hub.SubmitCollect(alice, collectibles[0].ID)
	_ = mustReceiveData(t, alice) // collect_result

	// 立即第二次拾取同一 ID（应被冷却拒绝）
	hub.SubmitCollect(alice, collectibles[0].ID)
	assertNoMessage(t, alice)
}

func TestHubCollectSyntheticRejection(t *testing.T) {
	hub, _, _, _ := newTestHubWithCollectibles()
	go hub.Run()
	defer hub.Stop()

	hub.syntheticPlayerIDs[1001] = struct{}{}

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	pos, _ := hub.world.PlayerPosition(1001)
	collectibles := hub.collectibleField.CollectiblesWithinRadius(pos.Lat, pos.Lng, 10)
	if len(collectibles) == 0 {
		t.Skip("10m 内无收集品")
	}

	hub.SubmitCollect(alice, collectibles[0].ID)
	assertNoMessage(t, alice)
}

func TestHubCollectStaleID(t *testing.T) {
	hub, _, _, _ := newTestHubWithCollectibles()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	// 提交不存在的收集品 ID
	hub.SubmitCollect(alice, 99999)
	// 不应 crash 或发送结果
}

func TestHubCollectObsoleteConnection(t *testing.T) {
	hub, _, _, _ := newTestHubWithCollectibles()
	go hub.Run()
	defer hub.Stop()

	alice := NewTestClient(1001, 8)
	hub.Register(alice)
	mustReceiveInitialization(t, alice)

	// 注册替换连接
	replacement := NewTestClient(1001, 8)
	hub.Register(replacement)
	mustReceiveInitialization(t, replacement)

	// 通过旧连接发送拾取（应被忽略）
	hub.SubmitCollect(alice, 1)
	// 不应 crash
}
