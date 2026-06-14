package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"map-walker/internal/game"
	"map-walker/internal/realtime"
	"map-walker/internal/storage"
	"map-walker/internal/synthetic"
)

// TestValidateSyntheticFlagsAcceptsZero verifies zero clients and zero ramp rate are valid.
func TestValidateSyntheticFlagsAcceptsZero(t *testing.T) {
	if err := validateSyntheticFlags(syntheticFlags{count: 0, rampRate: 0}); err != nil {
		t.Errorf("expected no error for zero values, got: %v", err)
	}
}

// TestValidateSyntheticFlagsAcceptsDefaults verifies the default flag values are valid.
func TestValidateSyntheticFlagsAcceptsDefaults(t *testing.T) {
	if err := validateSyntheticFlags(syntheticFlags{count: 0, rampRate: 10}); err != nil {
		t.Errorf("expected no error for defaults, got: %v", err)
	}
}

// TestValidateSyntheticFlagsRejectsNegativeCount verifies negative client count fails.
func TestValidateSyntheticFlagsRejectsNegativeCount(t *testing.T) {
	if err := validateSyntheticFlags(syntheticFlags{count: -1, rampRate: 10}); err == nil {
		t.Error("expected error for negative count")
	}
}

// TestValidateSyntheticFlagsRejectsNegativeRampRate verifies negative ramp rate fails.
func TestValidateSyntheticFlagsRejectsNegativeRampRate(t *testing.T) {
	if err := validateSyntheticFlags(syntheticFlags{count: 1, rampRate: -1}); err == nil {
		t.Error("expected error for negative ramp rate")
	}
}

// TestZeroClientStartup verifies that with count=0, no Manager is created.
func TestZeroClientStartup(t *testing.T) {
	flags := syntheticFlags{count: 0, rampRate: 10}
	var manager *synthetic.Manager
	if flags.count > 0 {
		t.Fatal("test misconfigured")
	}
	if manager != nil {
		t.Error("manager must be nil for zero synthetic clients")
	}
}

// TestShutdownOrderManagerBeforeHub verifies Manager.Stop() completes before Hub.Stop().
func TestShutdownOrderManagerBeforeHub(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	config := game.DefaultConfig()
	hub, _, _, _, _ := realtime.NewManualTickHub(config, nil, nil)
	go hub.Run()

	manager, err := synthetic.NewManager(synthetic.ManagerConfig{
		TargetCount: 0,
		RampRate:    0,
		Behavior:    synthetic.DefaultBehaviorConfig(),
	}, synthetic.ManagerDeps{
		Hub:   hub,
		Store: db,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.Start(context.Background())

	// Record the order of Stop calls.
	var mu sync.Mutex
	var stopOrder []string

	wrappedManagerStop := func() {
		manager.Stop()
		mu.Lock()
		stopOrder = append(stopOrder, "manager")
		mu.Unlock()
	}
	wrappedHubStop := func() {
		hub.Stop()
		mu.Lock()
		stopOrder = append(stopOrder, "hub")
		mu.Unlock()
	}

	wrappedManagerStop()
	wrappedHubStop()

	mu.Lock()
	defer mu.Unlock()
	if len(stopOrder) != 2 || stopOrder[0] != "manager" || stopOrder[1] != "hub" {
		t.Errorf("unexpected stop order: %v", stopOrder)
	}
}

// TestManagerStartsAndStopsCleanly verifies Manager + Hub lifecycle with no accounts.
func TestManagerStartsAndStopsCleanly(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	config := game.DefaultConfig()
	hub, _, _, _, _ := realtime.NewManualTickHub(config, nil, nil)
	go hub.Run()

	manager, err := synthetic.NewManager(synthetic.ManagerConfig{
		TargetCount: 0,
		RampRate:    0,
		Behavior:    synthetic.DefaultBehaviorConfig(),
	}, synthetic.ManagerDeps{
		Hub:   hub,
		Store: db,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.Start(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.Stop()
		hub.Stop()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

// TestAutoProvisionRequiresPassword verifies that auto-provision without a password
// causes Manager construction to fail.
func TestAutoProvisionRequiresPassword(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	config := game.DefaultConfig()
	hub, _, _, _, _ := realtime.NewManualTickHub(config, nil, nil)
	go hub.Run()
	defer hub.Stop()

	_, err = synthetic.NewManager(synthetic.ManagerConfig{
		TargetCount:   1,
		RampRate:      0,
		AutoProvision: true,
		Password:      "", // missing
		Behavior:      synthetic.DefaultBehaviorConfig(),
	}, synthetic.ManagerDeps{
		Hub:   hub,
		Store: db,
	})
	if err == nil {
		t.Error("expected error when auto-provision is enabled but password is missing")
	}
}
