package synthetic

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"map-walker/internal/realtime"
)

const readinessTimeout = 5 * time.Second

var (
	ErrMissingAccount     = errors.New("synthetic account missing")
	ErrReadinessTimeout   = errors.New("synthetic client readiness timeout")
	ErrHubUnavailable     = errors.New("hub unavailable")
	ErrClientDisconnected = errors.New("synthetic client disconnected")
	ErrManagerStopped     = errors.New("synthetic manager stopped")
)

type ManagerConfig struct {
	TargetCount      int
	RampRate         int
	AutoProvision    bool
	Password         string
	Behavior         BehaviorConfig
	ProvisionWorkers int
}

type ManagerDeps struct {
	Hub          *realtime.Hub
	Store        userStore
	Provisioner  *Provisioner
	NewClient    func(userID int64, username string) *Client
	Now          func() time.Time
	ManualTick   chan struct{}
	ManualTickAck chan struct{}
}

type ManagerStatus struct {
	Target      int
	Pending     int
	Activating  int
	Active      int
	Failed      int
	Disconnects uint64
}

type Manager struct {
	cfg  ManagerConfig
	deps ManagerDeps

	stop     chan struct{}
	done     chan struct{}
	runOnce  sync.Once
	stopOnce sync.Once

	provisionUpdates chan provisionResultMsg

	statusMu sync.RWMutex
	status   ManagerStatus
}

type provisionResultMsg struct {
	result ProvisionResult
	err    error
}

type accountState int

const (
	accountUnavailable accountState = iota
	accountPending
	accountActivating
	accountActive
	accountFailed
)

type accountIdentity struct {
	userID   int64
	username string
	lat      float64
	lng      float64
}

type accountSlot struct {
	accountNumber int
	identity      accountIdentity
	state         accountState
	err           error

	client      *Client
	behavior    *Behavior
	activatedAt time.Time
}

type managerLoop struct {
	*Manager
	slots           map[int]*accountSlot
	shuttingDown    bool
	rampTokens      float64
	disconnectCount uint64
}

func NewManager(cfg ManagerConfig, deps ManagerDeps) (*Manager, error) {
	if err := validateManagerConfig(cfg); err != nil {
		return nil, err
	}
	if deps.NewClient == nil {
		deps.NewClient = NewClient
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Manager{
		cfg:              cfg,
		deps:             deps,
		stop:             make(chan struct{}),
		done:             make(chan struct{}),
		provisionUpdates: make(chan provisionResultMsg, 1),
		status: ManagerStatus{
			Target: cfg.TargetCount,
		},
	}, nil
}

func (m *Manager) Start(ctx context.Context) {
	m.runOnce.Do(func() {
		go m.run(ctx)
	})
}

func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stop)
	})
	<-m.done
}

func (m *Manager) Status() ManagerStatus {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	return m.status
}

func (m *Manager) run(ctx context.Context) {
	defer close(m.done)

	loop := &managerLoop{
		Manager: m,
		slots:   make(map[int]*accountSlot, m.cfg.TargetCount),
	}
	for accountNumber := 1; accountNumber <= m.cfg.TargetCount; accountNumber++ {
		loop.slots[accountNumber] = &accountSlot{
			accountNumber: accountNumber,
			state:         accountUnavailable,
		}
	}

	if m.cfg.AutoProvision {
		workers := m.cfg.ProvisionWorkers
		if workers < 1 {
			workers = runtime.GOMAXPROCS(0)
		}
		go func() {
			result, err := m.deps.Provisioner.Provision(ctx, m.cfg.TargetCount, workers, m.cfg.Password)
			select {
			case m.provisionUpdates <- provisionResultMsg{result: result, err: err}:
			case <-m.stop:
			}
		}()
	} else {
		loop.loadExistingAccounts()
	}

	ticker := time.NewTicker(BehaviorTickInterval)
	defer ticker.Stop()
	useManualTick := m.deps.ManualTick != nil

	for {
		select {
		case <-m.stop:
			loop.shutdown()
			return
		case msg := <-m.provisionUpdates:
			if msg.err != nil {
				loop.failUnavailable(msg.err)
			} else {
				loop.mergeProvisionResult(msg.result)
			}
		case <-ticker.C:
			if !useManualTick {
				loop.onTick()
			}
		case <-m.deps.ManualTick:
			if useManualTick {
				loop.onTick()
				if m.deps.ManualTickAck != nil {
					m.deps.ManualTickAck <- struct{}{}
				}
			}
		}
	}
}

func (l *managerLoop) loadExistingAccounts() {
	records, err := l.deps.Store.LoadSyntheticUsers()
	if err != nil {
		l.failUnavailable(err)
		return
	}

	indexed := map[int]accountIdentity{}
	for _, record := range records {
		accountNumber, ok := ParseUsername(record.Username)
		if !ok {
			continue
		}
		lat, lng := record.Lat, record.Lng
		if !record.HasPosition {
			lat, lng = PlacementLatLng(l.cfg.Behavior.Placement, accountNumber)
		}
		indexed[accountNumber] = accountIdentity{
			userID:   record.UserID,
			username: record.Username,
			lat:      lat,
			lng:      lng,
		}
	}

	for accountNumber := 1; accountNumber <= l.cfg.TargetCount; accountNumber++ {
		slot := l.slots[accountNumber]
		identity, ok := indexed[accountNumber]
		if !ok {
			slot.state = accountFailed
			slot.err = ErrMissingAccount
			continue
		}
		slot.identity = identity
		slot.state = accountPending
	}
	l.publishStatus()
}

func (l *managerLoop) mergeProvisionResult(result ProvisionResult) {
	for _, account := range result.Accounts {
		slot := l.slots[account.AccountNumber]
		if account.Err != nil {
			slot.state = accountFailed
			slot.err = account.Err
			continue
		}
		slot.identity = accountIdentity{
			userID:   account.UserID,
			username: account.Username,
			lat:      account.Lat,
			lng:      account.Lng,
		}
		slot.state = accountPending
	}
	l.publishStatus()
}

func (l *managerLoop) failUnavailable(err error) {
	for accountNumber := 1; accountNumber <= l.cfg.TargetCount; accountNumber++ {
		slot := l.slots[accountNumber]
		if slot.state == accountUnavailable || (slot.state == accountPending && slot.identity.userID == 0) {
			slot.state = accountFailed
			slot.err = err
		}
	}
	l.publishStatus()
}

func (l *managerLoop) onTick() {
	if l.shuttingDown {
		return
	}

	l.addRampTokens()
	l.processActivatingAndActive()
	l.tryActivateNext()
	l.publishStatus()
}

func (l *managerLoop) addRampTokens() {
	if l.cfg.RampRate <= 0 {
		return
	}
	l.rampTokens += float64(l.cfg.RampRate) * BehaviorTickInterval.Seconds()
}

func (l *managerLoop) tryActivateNext() {
	for {
		accountNumber := l.nextPendingAccount()
		if accountNumber == 0 {
			return
		}
		if l.cfg.RampRate > 0 {
			if l.rampTokens < 1 {
				return
			}
			l.rampTokens -= 1
		}
		l.startActivation(accountNumber)
	}
}

func (l *managerLoop) nextPendingAccount() int {
	for accountNumber := 1; accountNumber <= l.cfg.TargetCount; accountNumber++ {
		if l.slots[accountNumber].state == accountPending {
			return accountNumber
		}
	}
	return 0
}

func (l *managerLoop) startActivation(accountNumber int) {
	slot := l.slots[accountNumber]
	client := l.deps.NewClient(slot.identity.userID, slot.identity.username)
	if !l.deps.Hub.Register(client) {
		client.CloseSend()
		slot.state = accountFailed
		slot.err = ErrHubUnavailable
		return
	}

	slot.client = client
	slot.behavior = NewBehavior(accountNumber, l.cfg.Behavior, slot.identity.lat, slot.identity.lng)
	slot.state = accountActivating
	slot.activatedAt = l.deps.Now()
}

func (l *managerLoop) processActivatingAndActive() {
	for accountNumber := 1; accountNumber <= l.cfg.TargetCount; accountNumber++ {
		slot := l.slots[accountNumber]
		switch slot.state {
		case accountActivating:
			l.processActivating(accountNumber, slot)
		case accountActive:
			l.processActive(accountNumber, slot)
		}
	}
}

func (l *managerLoop) processActivating(accountNumber int, slot *accountSlot) {
	if l.clientDisconnected(slot.client) {
		l.failSlot(accountNumber, slot, ErrClosedBeforeReady)
		return
	}
	if clientReady(slot.client) {
		slot.state = accountActive
		return
	}
	if l.deps.Now().Sub(slot.activatedAt) >= readinessTimeout {
		l.failSlot(accountNumber, slot, ErrReadinessTimeout)
	}
}

func (l *managerLoop) processActive(accountNumber int, slot *accountSlot) {
	if l.clientDisconnected(slot.client) {
		l.handleUnexpectedDisconnect(accountNumber, slot)
		return
	}

	input, changed := slot.behavior.OnTick()
	if changed {
		l.deps.Hub.ApplyInput(slot.client, input)
	}
}

func (l *managerLoop) failSlot(accountNumber int, slot *accountSlot, err error) {
	if slot.client != nil {
		l.deps.Hub.Unregister(slot.client)
		slot.client.CloseSend()
		slot.client = nil
	}
	slot.behavior = nil
	slot.state = accountFailed
	slot.err = err
}

func (l *managerLoop) handleUnexpectedDisconnect(accountNumber int, slot *accountSlot) {
	if l.shuttingDown {
		return
	}
	l.disconnectCount++
	l.failSlot(accountNumber, slot, ErrClientDisconnected)
}

func (l *managerLoop) clientDisconnected(client *Client) bool {
	select {
	case <-client.Done():
		return true
	default:
		return false
	}
}

func (l *managerLoop) shutdown() {
	if l.shuttingDown {
		return
	}
	l.shuttingDown = true

	for accountNumber := 1; accountNumber <= l.cfg.TargetCount; accountNumber++ {
		slot := l.slots[accountNumber]
		if slot.client == nil {
			continue
		}
		if slot.state == accountActivating || slot.state == accountActive {
			l.deps.Hub.Unregister(slot.client)
			slot.client.CloseSend()
		}
	}

	for accountNumber := 1; accountNumber <= l.cfg.TargetCount; accountNumber++ {
		slot := l.slots[accountNumber]
		if slot.client == nil {
			continue
		}
		<-slot.client.Done()
		slot.client = nil
		slot.behavior = nil
	}

	l.publishStatus()
}

func (l *managerLoop) publishStatus() {
	status := ManagerStatus{
		Target:      l.cfg.TargetCount,
		Disconnects: l.disconnectCount,
	}
	for accountNumber := 1; accountNumber <= l.cfg.TargetCount; accountNumber++ {
		switch l.slots[accountNumber].state {
		case accountPending, accountUnavailable:
			status.Pending++
		case accountActivating:
			status.Activating++
		case accountActive:
			status.Active++
		case accountFailed:
			status.Failed++
		}
	}

	l.statusMu.Lock()
	l.status = status
	l.statusMu.Unlock()
}

func clientReady(client *Client) bool {
	select {
	case <-client.Ready():
		return true
	default:
		return client.MessagesDrained() >= initializationMessagesRequired
	}
}

func validateManagerConfig(cfg ManagerConfig) error {
	if cfg.TargetCount < 0 {
		return fmt.Errorf("target count must be non-negative")
	}
	if cfg.RampRate < 0 {
		return fmt.Errorf("ramp rate must be non-negative")
	}
	if cfg.AutoProvision && cfg.Password == "" {
		return fmt.Errorf("password required for auto-provisioning")
	}
	return nil
}
