package synthetic

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"map-walker/internal/game"
	"map-walker/internal/realtime"
	"map-walker/internal/storage"
)

func TestManagerActivatesInAscendingOrderWithRamp(t *testing.T) {
	accounts := []testAccount{
		{accountNumber: 1},
		{accountNumber: 2},
		{accountNumber: 3},
	}
	env := startManagerTest(t, managerTestOptions{
		targetCount: 3,
		rampRate:    10,
		accounts:    accounts,
	})
	defer env.cleanup()

	env.tickManager()
	if got := env.manager.Status().Activating; got != 1 {
		t.Fatalf("after first tick activating=%d want 1", got)
	}
	if got := env.manager.Status().Active; got != 0 {
		t.Fatalf("after first tick active=%d want 0", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for env.manager.Status().Active < 1 && time.Now().Before(deadline) {
		env.tickManager()
		env.driveSimulation()
	}
	if got := env.manager.Status().Active; got < 1 {
		t.Fatalf("expected first account active, status=%+v", env.manager.Status())
	}

	for i := 0; i < 20; i++ {
		env.tickManager()
		env.driveSimulation()
	}
	if got := env.manager.Status().Active; got != 3 {
		t.Fatalf("expected all active, status=%+v", env.manager.Status())
	}
}

func TestManagerUnlimitedRampActivatesQuickly(t *testing.T) {
	accounts := []testAccount{
		{accountNumber: 1},
		{accountNumber: 2},
	}
	env := startManagerTest(t, managerTestOptions{
		targetCount: 2,
		rampRate:    0,
		accounts:    accounts,
	})
	defer env.cleanup()

	env.tickManager()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := env.manager.Status()
		if status.Activating+status.Active >= 2 {
			break
		}
		env.tickManager()
		env.driveSimulation()
	}
	status := env.manager.Status()
	if got := status.Activating + status.Active; got < 2 {
		t.Fatalf("activating+active=%d want at least 2, status=%+v", got, status)
	}
}

func TestManagerMissingAccountFailsWithoutAutoProvision(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount: 2,
		rampRate:    0,
		accounts: []testAccount{
			{accountNumber: 2},
		},
	})
	defer env.cleanup()

	waitForManagerStatus(t, env, func(status ManagerStatus) bool {
		return status.Failed+status.Pending == status.Target
	})

	if got := env.manager.Status().Failed; got != 1 {
		t.Fatalf("failed=%d want 1, status=%+v", got, env.manager.Status())
	}
	if got := env.manager.Status().Pending; got != 1 {
		t.Fatalf("pending=%d want 1", got)
	}
}

func TestManagerReadinessTimeout(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	advanceNow := func() {
		now = now.Add(BehaviorTickInterval)
	}
	env := startManagerTest(t, managerTestOptions{
		targetCount: 1,
		rampRate:    0,
		accounts: []testAccount{
			{accountNumber: 1},
		},
		now: func() time.Time {
			return now
		},
		newClient: func(userID int64, username string) *Client {
			client := NewClient(userID, username)
			client.drainDelay = readinessTimeout + time.Second
			return client
		},
	})
	defer env.cleanup()

	env.tickManager()
	for env.manager.Status().Failed == 0 {
		advanceNow()
		env.tickManager()
		if now.Sub(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)) >= readinessTimeout+BehaviorTickInterval {
			break
		}
	}
	if got := env.manager.Status().Failed; got != 1 {
		t.Fatalf("failed=%d want 1", got)
	}
}

func TestManagerAppliesInputToWorld(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount: 1,
		rampRate:    0,
		accounts: []testAccount{
			{accountNumber: 1},
		},
		fastMovement: true,
	})
	defer env.cleanup()

	env.activateAll(t)
	for i := 0; i < 50; i++ {
		env.tickManager()
		env.driveSimulation()
	}

	if len(env.persister.syncUpdates()) == 0 && len(env.persister.asyncUpdateBatches()) == 0 {
		t.Fatal("expected persisted movement updates")
	}
}

func TestManagerShutdownIsIdempotent(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount: 1,
		rampRate:    0,
		accounts: []testAccount{
			{accountNumber: 1},
		},
	})
	defer env.cleanup()

	env.activateAll(t)
	env.manager.Stop()
	env.manager.Stop()
	if got := env.manager.Status().Failed; got != 0 {
		t.Fatalf("shutdown counted as failure: failed=%d", got)
	}
}

func TestManagerQueueFullRemovalPersistsFinalPosition(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount: 1,
		rampRate:    0,
		accounts: []testAccount{
			{accountNumber: 1},
		},
		newClient: func(userID int64, username string) *Client {
			return NewClientWithHeldDrain(userID, username, 1)
		},
	})
	defer env.cleanup()

	deadline := time.Now().Add(2 * time.Second)
	for env.manager.Status().Failed == 0 && time.Now().Before(deadline) {
		env.tickManager()
		env.driveSimulation()
	}
	if got := env.manager.Status().Failed; got != 1 {
		t.Fatalf("failed=%d want 1, status=%+v", got, env.manager.Status())
	}
	if len(env.persister.syncUpdates()) == 0 {
		t.Fatal("expected final persistence on queue-full removal")
	}
	if got := env.manager.Status().Active; got != 0 {
		t.Fatalf("active=%d want 0 after queue-full removal (no reconnect)", got)
	}
}

func TestManagerStopWaitsForClientDrain(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount: 1,
		rampRate:    0,
		accounts: []testAccount{
			{accountNumber: 1},
		},
	})
	defer env.cleanup()

	env.activateAll(t)
	done := make(chan struct{})
	go func() {
		env.manager.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("manager stop blocked waiting for client drain")
	}
}

type testAccount struct {
	accountNumber int
	userID        int64
}

type managerTestOptions struct {
	targetCount  int
	rampRate     int
	accounts     []testAccount
	nearbySpawn  bool
	newClient    func(userID int64, username string) *Client
	fastMovement bool
	now          func() time.Time
}

type managerTestEnv struct {
	manager            *Manager
	hub                *realtime.Hub
	simulation         chan time.Time
	broadcast          chan time.Time
	persist            chan time.Time
	manualTick         chan struct{}
	manualTickAck      chan struct{}
	manualStatsTick    chan struct{}
	manualStatsTickAck chan struct{}
	persister          *recordingPersister
	cleanup            func()
}

func startManagerTest(t *testing.T, opts managerTestOptions) *managerTestEnv {
	t.Helper()

	if opts.targetCount == 0 {
		opts.targetCount = len(opts.accounts)
	}

	db, resolvedAccounts := seedTestAccounts(t, opts.accounts)

	config := game.DefaultConfig()
	if opts.fastMovement {
		config.SpeedMetersPerSecond = 3000
	}

	loader := testAccountLoader(resolvedAccounts)
	if opts.nearbySpawn {
		loader = nearbyAccountLoader(resolvedAccounts, config)
	}

	persister := &recordingPersister{}
	hub, simulation, broadcast, persist, _ := realtime.NewManualTickHub(config, loader, persister)
	go hub.Run()

	manualTick := make(chan struct{}, 32)
	manualTickAck := make(chan struct{}, 1)
	manualStatsTick := make(chan struct{}, 1)
	manualStatsTickAck := make(chan struct{}, 1)
	deps := ManagerDeps{
		Hub:                hub,
		Store:              db,
		NewClient:          opts.newClient,
		ManualTick:         manualTick,
		ManualTickAck:      manualTickAck,
		ManualStatsTick:    manualStatsTick,
		ManualStatsTickAck: manualStatsTickAck,
	}
	if opts.now != nil {
		deps.Now = opts.now
	}
	manager, err := NewManager(ManagerConfig{
		TargetCount: opts.targetCount,
		RampRate:    opts.rampRate,
		Behavior:    DefaultBehaviorConfig(),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	manager.Start(context.Background())

	env := &managerTestEnv{
		manager:            manager,
		hub:                hub,
		simulation:         simulation,
		broadcast:          broadcast,
		persist:            persist,
		manualTick:         manualTick,
		manualTickAck:      manualTickAck,
		manualStatsTick:    manualStatsTick,
		manualStatsTickAck: manualStatsTickAck,
		persister:          persister,
	}
	env.cleanup = func() {
		manager.Stop()
		hub.Stop()
	}
	return env
}

func (env *managerTestEnv) tickManager() {
	env.manualTick <- struct{}{}
	<-env.manualTickAck
}

func (env *managerTestEnv) tickStats() {
	env.manualStatsTick <- struct{}{}
	<-env.manualStatsTickAck
}

func (env *managerTestEnv) driveSimulation() {
	now := time.Now()
	env.simulation <- now
	env.broadcast <- now
	env.persist <- now
}

func (env *managerTestEnv) activateAll(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for env.manager.Status().Active < env.manager.Status().Target && time.Now().Before(deadline) {
		env.tickManager()
		env.driveSimulation()
	}
	if got := env.manager.Status().Active; got != env.manager.Status().Target {
		t.Fatalf("activateAll: active=%d target=%d status=%+v", got, env.manager.Status().Target, env.manager.Status())
	}
}

func waitForManagerStatus(t *testing.T, env *managerTestEnv, ok func(ManagerStatus) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ok(env.manager.Status()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for manager status: %+v", env.manager.Status())
}

func testAccountLoader(accounts []testAccount) realtime.SavedPlayerLoader {
	byID := map[int64]testAccount{}
	for _, account := range accounts {
		byID[account.userID] = account
	}
	appearance := FixedAppearance()
	return func(userID int64) (realtime.SavedPlayerLoad, bool) {
		account, ok := byID[userID]
		if !ok {
			return realtime.SavedPlayerLoad{}, false
		}
		lat, lng := PlacementLatLng(DefaultPlacementConfig(), account.accountNumber)
		return realtime.SavedPlayerLoad{
			Username:    FormatUsername(account.accountNumber),
			Lat:         lat,
			Lng:         lng,
			HasPosition: true,
			Appearance:  game.Appearance{Color: appearance.Color, Shape: appearance.Shape},
		}, true
	}
}

func seedTestAccounts(t *testing.T, accounts []testAccount) (*storage.DB, []testAccount) {
	t.Helper()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "manager-test.db"))
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	resolved := make([]testAccount, 0, len(accounts))
	for _, account := range accounts {
		lat, lng := PlacementLatLng(DefaultPlacementConfig(), account.accountNumber)
		id, err := db.CreateUser(storage.User{
			Username:           FormatUsername(account.accountNumber),
			UsernameNormalized: FormatUsername(account.accountNumber),
			PasswordHash:       "hash",
			CreatedAt:          now,
			LastLat:            sqlNullFloat(lat),
			LastLng:            sqlNullFloat(lng),
			Appearance:         FixedAppearance(),
		})
		if err != nil {
			t.Fatalf("create user failed: %v", err)
		}
		resolved = append(resolved, testAccount{
			accountNumber: account.accountNumber,
			userID:        id,
		})
	}
	return db, resolved
}

func sqlNullFloat(value float64) sql.NullFloat64 {
	return sql.NullFloat64{Valid: true, Float64: value}
}

type recordingPersister struct {
	mu           sync.Mutex
	asyncUpdates [][]realtime.PositionUpdate
	synced       [][]realtime.PositionUpdate
}

func (p *recordingPersister) Submit(updates []realtime.PositionUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copied := append([]realtime.PositionUpdate(nil), updates...)
	p.asyncUpdates = append(p.asyncUpdates, copied)
}

func (p *recordingPersister) SubmitSync(updates []realtime.PositionUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copied := append([]realtime.PositionUpdate(nil), updates...)
	p.synced = append(p.synced, copied)
}

func (p *recordingPersister) Stop() {}

func (p *recordingPersister) Drain() {}

func (p *recordingPersister) asyncUpdateBatches() [][]realtime.PositionUpdate {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]realtime.PositionUpdate(nil), p.asyncUpdates...)
}

func (p *recordingPersister) syncUpdates() [][]realtime.PositionUpdate {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]realtime.PositionUpdate(nil), p.synced...)
}

func TestManagerClientsReceiveInitializationAndReplication(t *testing.T) {
	var clients []*Client
	accounts := []testAccount{
		{accountNumber: 1},
		{accountNumber: 2},
	}
	env := startManagerTest(t, managerTestOptions{
		targetCount:  2,
		rampRate:     0,
		accounts:     accounts,
		nearbySpawn:  true,
		fastMovement: true,
		newClient: func(userID int64, username string) *Client {
			client := NewClient(userID, username)
			clients = append(clients, client)
			return client
		},
	})
	defer env.cleanup()

	env.activateAll(t)
	for i, client := range clients {
		if !env.hub.ApplyInput(client, game.InputState{Sequence: 1, Right: true}) {
			t.Fatalf("ApplyInput failed for client %d", i)
		}
	}
	for i := 0; i < 30; i++ {
		env.tickManager()
		env.driveSimulation()
	}

	for i, client := range clients {
		if got := client.MessagesDrained(); got <= initializationMessagesRequired {
			t.Fatalf("client %d drained=%d want replication beyond initialization", i, got)
		}
	}
}

func nearbyAccountLoader(accounts []testAccount, spawn game.Config) realtime.SavedPlayerLoader {
	byID := map[int64]testAccount{}
	for _, account := range accounts {
		byID[account.userID] = account
	}
	appearance := FixedAppearance()
	offsetMeters := 100.0
	return func(userID int64) (realtime.SavedPlayerLoad, bool) {
		account, ok := byID[userID]
		if !ok {
			return realtime.SavedPlayerLoad{}, false
		}
		lat := spawn.SpawnLat
		lng := spawn.SpawnLng
		if account.accountNumber == 2 {
			lat += offsetMeters / 111_000
		}
		return realtime.SavedPlayerLoad{
			Username:    FormatUsername(account.accountNumber),
			Lat:         lat,
			Lng:         lng,
			HasPosition: true,
			Appearance:  game.Appearance{Color: appearance.Color, Shape: appearance.Shape},
		}, true
	}
}

func TestManagerHubStopFailsActivation(t *testing.T) {
	env := startManagerTest(t, managerTestOptions{
		targetCount: 1,
		rampRate:    0,
		accounts: []testAccount{
			{accountNumber: 1},
		},
	})
	env.hub.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for env.manager.Status().Failed == 0 && time.Now().Before(deadline) {
		env.tickManager()
	}
	if got := env.manager.Status().Failed; got != 1 {
		t.Fatalf("failed=%d want 1", got)
	}
	env.manager.Stop()
}

func TestManagerUnexpectedDisconnectCountsFailure(t *testing.T) {
	var activeClient *Client
	env := startManagerTest(t, managerTestOptions{
		targetCount: 1,
		rampRate:    0,
		accounts: []testAccount{
			{accountNumber: 1},
		},
		newClient: func(userID int64, username string) *Client {
			activeClient = NewClient(userID, username)
			return activeClient
		},
	})
	defer env.cleanup()
	env.activateAll(t)
	activeClient.CloseSend()
	time.Sleep(10 * time.Millisecond)
	env.tickManager()
	if got := env.manager.Status().Failed; got != 1 {
		t.Fatalf("failed=%d want 1", got)
	}
	if got := env.manager.Status().Disconnects; got != 1 {
		t.Fatalf("disconnects=%d want 1", got)
	}
}

func TestManagerAutoProvisionPartialFailure(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "manager-auto.db"))
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	provisioner := newTestProvisioner(db)
	provisioner.Store = &faultInjectStore{
		db: db,
		failAccounts: map[int]error{
			2: errors.New("injected"),
		},
	}

	config := game.DefaultConfig()
	persister := &recordingPersister{}
	hub, simulation, broadcast, persist, _ := realtime.NewManualTickHub(config, testAccountLoader(nil), persister)
	go hub.Run()
	_ = simulation
	_ = broadcast
	_ = persist

	manualTick := make(chan struct{}, 8)
	manualTickAck := make(chan struct{}, 1)
	manager, err := NewManager(ManagerConfig{
		TargetCount:   2,
		RampRate:      0,
		AutoProvision: true,
		Password:      testPassword,
		Behavior:      DefaultBehaviorConfig(),
	}, ManagerDeps{
		Hub:           hub,
		Provisioner:   provisioner,
		ManualTick:    manualTick,
		ManualTickAck: manualTickAck,
	})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	manager.Start(context.Background())
	defer func() {
		manager.Stop()
		hub.Stop()
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.Status()
		if status.Failed == 1 && status.Pending == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := manager.Status().Failed; got != 1 {
		t.Fatalf("failed=%d want 1, status=%+v", got, manager.Status())
	}
	if got := manager.Status().Pending; got != 1 {
		t.Fatalf("pending=%d want 1, status=%+v", got, manager.Status())
	}

	manualTick <- struct{}{}
	<-manualTickAck
	status := manager.Status()
	if status.Failed != 1 {
		t.Fatalf("failed=%d want 1, status=%+v", status.Failed, status)
	}
	if status.Pending != 0 && status.Activating != 1 && status.Active != 1 {
		t.Fatalf("expected account 1 to begin activation, status=%+v", status)
	}
}
