package realtime

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"map-walker/internal/game"
)

const (
	simulationInterval  = 50 * time.Millisecond
	broadcastInterval   = 100 * time.Millisecond
	persistenceInterval = 5 * time.Second
	statsInterval       = time.Second
)

type ClientSender interface {
	ID() int64
	Username() string
	Send([]byte) bool
	CloseSend()
}

type disconnectRequest struct {
	userID int64
	done   chan struct{}
}

type inputEvent struct {
	client ClientSender
	input  game.InputState
}

type appearanceUpdateRequest struct {
	userID     int64
	appearance game.Appearance
	done       chan struct{}
}

type Hub struct {
	world              *game.World
	aoi                *game.AOIIndex
	loadSavedPlayer    SavedPlayerLoader
	persister          PositionPersister
	register           chan ClientSender
	unregister         chan ClientSender
	inputs             chan inputEvent
	appearanceUpdates  chan appearanceUpdateRequest
	stop               chan struct{}
	done               chan struct{}
	stopOnce           sync.Once
	clients            map[int64]ClientSender
	persistDirty       map[int64]struct{}
	persistSeq         map[int64]uint64
	pendingEntered     map[int64]game.PlayerState
	pendingLeft        map[int64][]int64
	pendingAppearances map[int64]game.Appearance
	disconnectUser     chan disconnectRequest
	simulationTick     <-chan time.Time
	broadcastTick      <-chan time.Time
	persistenceTick    <-chan time.Time
	statsTick          <-chan time.Time
	stopTickers        func()
	stats              intervalStats
	snapshot           atomic.Pointer[HubSnapshot]
}

func (h *Hub) Snapshot() *HubSnapshot {
	return h.snapshot.Load()
}

type intervalStats struct {
	acceptedInputs          uint64
	simulationTicks         uint64
	movedPlayers            uint64
	aoiCandidatePairs       uint64
	aoiDistanceChecks       uint64
	aoiRelationshipsEntered uint64
	aoiRelationshipsLeft    uint64
	replicationMessages     uint64
	replicationRecipients   uint64
	replicationBytes        uint64
}

func NewHub() *Hub {
	return NewHubWithSavedPositions(nil, nil)
}

func NewHubWithSavedPositions(loader SavedPlayerLoader, persister PositionPersister) *Hub {
	simulationTicker := time.NewTicker(simulationInterval)
	broadcastTicker := time.NewTicker(broadcastInterval)
	persistenceTicker := time.NewTicker(persistenceInterval)
	statsTicker := time.NewTicker(statsInterval)

	return newHub(
		game.NewWorld(game.DefaultConfig()),
		loader,
		persister,
		simulationTicker.C,
		broadcastTicker.C,
		persistenceTicker.C,
		statsTicker.C,
		func() {
			simulationTicker.Stop()
			broadcastTicker.Stop()
			persistenceTicker.Stop()
			statsTicker.Stop()
		},
	)
}

func newHub(
	world *game.World,
	loadSavedPlayer SavedPlayerLoader,
	persister PositionPersister,
	simulationTick <-chan time.Time,
	broadcastTick <-chan time.Time,
	persistenceTick <-chan time.Time,
	statsTick <-chan time.Time,
	stopTickers func(),
) *Hub {
	return &Hub{
		world:              world,
		aoi:                game.NewAOIIndex(game.AOIConfigFromWorld(world.Config())),
		loadSavedPlayer:    loadSavedPlayer,
		persister:          persister,
		register:           make(chan ClientSender),
		unregister:         make(chan ClientSender),
		inputs:             make(chan inputEvent),
		appearanceUpdates:  make(chan appearanceUpdateRequest),
		stop:               make(chan struct{}),
		done:               make(chan struct{}),
		clients:            map[int64]ClientSender{},
		persistDirty:       map[int64]struct{}{},
		persistSeq:         map[int64]uint64{},
		pendingEntered:     map[int64]game.PlayerState{},
		pendingLeft:        map[int64][]int64{},
		pendingAppearances: map[int64]game.Appearance{},
		disconnectUser:     make(chan disconnectRequest),
		simulationTick:     simulationTick,
		broadcastTick:      broadcastTick,
		persistenceTick:    persistenceTick,
		statsTick:          statsTick,
		stopTickers:        stopTickers,
	}
}

// Run is the single owner of both connections and authoritative world state.
func (h *Hub) Run() {
	defer close(h.done)
	defer h.stopTickers()

	for {
		select {
		case client := <-h.register:
			h.registerClient(client)
		case client := <-h.unregister:
			h.removeClient(client)
		case event := <-h.inputs:
			if h.clients[event.client.ID()] == event.client &&
				h.world.ApplyInput(event.client.ID(), event.input) {
				h.stats.acceptedInputs++
			}
		case <-h.simulationTick:
			moved := h.world.Step(simulationInterval)
			for _, playerID := range moved {
				h.persistDirty[playerID] = struct{}{}
			}
			h.stats.simulationTicks++
		case <-h.broadcastTick:
			h.broadcastReplication()
		case <-h.persistenceTick:
			h.persistDirtyPositions()
		case <-h.statsTick:
			h.logStats()
		case req := <-h.disconnectUser:
			if client, ok := h.clients[req.userID]; ok {
				h.removeClient(client)
			}
			if d, ok := h.persister.(PositionDrainer); ok {
				d.Drain()
			}
			close(req.done)
		case req := <-h.appearanceUpdates:
			h.applyAppearanceUpdate(req)
		case <-h.stop:
			for _, client := range h.clients {
				h.submitFinalPosition(client.ID())
			}
			if d, ok := h.persister.(PositionDrainer); ok {
				d.Drain()
			}
			for _, client := range h.clients {
				client.CloseSend()
			}
			return
		}
	}
}

func (h *Hub) Stop() {
	h.stopOnce.Do(func() {
		close(h.stop)
	})
	<-h.done
	if h.persister != nil {
		h.persister.Stop()
	}
}

func (h *Hub) DisconnectUser(userID int64) {
	done := make(chan struct{})
	select {
	case h.disconnectUser <- disconnectRequest{userID: userID, done: done}:
		<-done
	case <-h.done:
	}
}

func (h *Hub) Register(client ClientSender) bool {
	select {
	case h.register <- client:
		return true
	case <-h.done:
		return false
	}
}

func (h *Hub) Unregister(client ClientSender) {
	select {
	case h.unregister <- client:
	case <-h.done:
	}
}

func (h *Hub) ApplyInput(client ClientSender, input game.InputState) bool {
	select {
	case h.inputs <- inputEvent{client: client, input: input}:
		return true
	case <-h.done:
		return false
	}
}

func (h *Hub) UpdateAppearance(userID int64, appearance game.Appearance) bool {
	done := make(chan struct{})
	select {
	case h.appearanceUpdates <- appearanceUpdateRequest{
		userID:     userID,
		appearance: appearance,
		done:       done,
	}:
		<-done
		return true
	case <-h.done:
		return false
	}
}

func (h *Hub) registerClient(client ClientSender) {
	if existing, exists := h.clients[client.ID()]; exists && existing != client {
		existing.CloseSend()
		h.world.ResetInput(client.ID())
		h.clients[client.ID()] = client
		h.clearPendingReplicationFor(client.ID())
		h.sendInitialization(client)
		return
	}

	if !h.world.HasPlayer(client.ID()) {
		h.addPlayer(client.ID(), client.Username())
		h.insertPlayerIntoAOI(client.ID())
		if len(h.clients) > 0 {
			if state, ok := h.world.PlayerState(client.ID()); ok {
				h.pendingEntered[client.ID()] = state
				h.clearPendingLeftForPlayer(client.ID())
			}
		}
	}

	h.clients[client.ID()] = client
	h.clearPendingReplicationFor(client.ID())
	h.sendInitialization(client)
}

func (h *Hub) insertPlayerIntoAOI(playerID int64) {
	position, ok := h.world.PlayerPosition(playerID)
	if !ok {
		return
	}
	h.aoi.Insert(playerID, position.Lat, position.Lng)
}

func (h *Hub) clearPendingReplicationFor(playerID int64) {
	delete(h.pendingAppearances, playerID)
	delete(h.pendingLeft, playerID)
}

func (h *Hub) clearPendingLeftForPlayer(playerID int64) {
	for clientID, leftIDs := range h.pendingLeft {
		filtered := leftIDs[:0]
		for _, id := range leftIDs {
			if id != playerID {
				filtered = append(filtered, id)
			}
		}
		if len(filtered) == 0 {
			delete(h.pendingLeft, clientID)
		} else {
			h.pendingLeft[clientID] = filtered
		}
	}
}

func (h *Hub) isVisibleTo(clientID, playerID int64) bool {
	return h.aoi.IsVisible(clientID, playerID)
}

func (h *Hub) addPlayer(userID int64, username string) {
	if h.loadSavedPlayer != nil {
		if state, ok := h.loadSavedPlayer(userID); ok {
			lat, lng := state.Lat, state.Lng
			if !state.HasPosition {
				lat, lng = h.world.SpawnLatLng()
			}
			playerUsername := state.Username
			if playerUsername == "" {
				playerUsername = username
			}
			h.world.AddPlayerWithState(userID, playerUsername, lat, lng, state.Appearance)
			return
		}
	}
	lat, lng := h.world.SpawnLatLng()
	h.world.AddPlayerWithState(userID, username, lat, lng, game.DefaultAppearance())
}

func (h *Hub) applyAppearanceUpdate(req appearanceUpdateRequest) {
	defer close(req.done)

	if !h.world.HasPlayer(req.userID) {
		return
	}

	changed, ok := h.world.UpdatePlayerAppearance(req.userID, req.appearance)
	if !ok || !changed {
		return
	}

	h.pendingAppearances[req.userID] = req.appearance
}

func (h *Hub) removeClient(client ClientSender) {
	current, exists := h.clients[client.ID()]
	if !exists || current != client {
		return
	}

	delete(h.clients, client.ID())
	h.submitFinalPosition(client.ID())

	changes := h.aoi.Remove(client.ID())
	for _, neighborID := range changes.Left {
		if _, connected := h.clients[neighborID]; connected {
			h.pendingLeft[neighborID] = append(h.pendingLeft[neighborID], client.ID())
		}
	}

	h.world.RemovePlayer(client.ID())
	delete(h.pendingEntered, client.ID())
	h.clearPendingReplicationFor(client.ID())
	delete(h.persistDirty, client.ID())
	delete(h.persistSeq, client.ID())
	client.CloseSend()
}

func (h *Hub) persistDirtyPositions() {
	if h.persister == nil || len(h.persistDirty) == 0 {
		return
	}

	updates := make([]PositionUpdate, 0, len(h.persistDirty))
	for userID := range h.persistDirty {
		position, ok := h.world.PlayerPosition(userID)
		if !ok {
			continue
		}
		h.persistSeq[userID]++
		updates = append(updates, PositionUpdate{
			UserID: userID,
			Lat:    position.Lat,
			Lng:    position.Lng,
			Seq:    h.persistSeq[userID],
		})
	}
	clear(h.persistDirty)
	h.persister.Submit(updates)
}

func (h *Hub) submitFinalPosition(userID int64) {
	if h.persister == nil {
		return
	}
	position, ok := h.world.PlayerPosition(userID)
	if !ok {
		return
	}
	h.persistSeq[userID]++
	update := []PositionUpdate{{
		UserID: userID,
		Lat:    position.Lat,
		Lng:    position.Lng,
		Seq:    h.persistSeq[userID],
	}}
	if syncSub, ok := h.persister.(interface{ SubmitSync([]PositionUpdate) }); ok {
		syncSub.SubmitSync(update)
	} else {
		h.persister.Submit(update)
	}
}

func (h *Hub) sendInitialization(client ClientSender) {
	tick := h.world.Tick()
	self, ok := h.world.PlayerState(client.ID())
	if !ok {
		h.removeClient(client)
		return
	}

	selfData, err := EncodeSelfState(tick, self, 0)
	if err != nil {
		log.Printf("encode self state failed: %v", err)
		h.removeClient(client)
		return
	}
	if ok := client.Send(selfData); !ok {
		h.removeClient(client)
		return
	}

	visibleIDs := h.aoi.VisibleNeighbors(client.ID())
	visibleData, err := EncodeVisibleEntitiesSnapshot(tick, h.world.PlayerStates(visibleIDs))
	if err != nil {
		log.Printf("encode visible entities snapshot failed: %v", err)
		h.removeClient(client)
		return
	}
	if ok := client.Send(visibleData); !ok {
		h.removeClient(client)
	}
}

func (h *Hub) broadcastReplication() {
	movedIDs := h.world.TakeMovedPlayerIDs()
	h.world.TakeRemovedPlayerIDs()

	oldNeighborsByMover := h.snapshotMoverVisibility(movedIDs)
	h.applyMovementAOIChanges(movedIDs)
	h.stats.movedPlayers += uint64(len(movedIDs))

	pendingEntered := h.takePendingEntered()
	pendingLeft := h.takePendingLeft()
	pendingAppearances := h.takePendingAppearances()

	if len(movedIDs) == 0 && len(pendingEntered) == 0 && len(pendingLeft) == 0 && len(pendingAppearances) == 0 {
		return
	}

	tick := h.world.Tick()
	byRecipient := make(map[int64]*ReplicationChanges)

	// 自位置：每个已连接的移动者
	for _, playerID := range movedIDs {
		if _, connected := h.clients[playerID]; !connected {
			continue
		}
		if position, ok := h.world.PlayerPosition(playerID); ok {
			entry := getOrCreateRecipient(byRecipient, playerID)
			entry.SelfPosition = &SelfPosition{Lat: position.Lat, Lng: position.Lng}
		}
	}

	// 稳定关系位置：从移动者扇出到旧邻居（同时仍在最终可见集中）
	for _, moverID := range movedIDs {
		position, ok := h.world.PlayerPosition(moverID)
		if !ok {
			continue
		}
		oldNeighbors := oldNeighborsByMover[moverID]
		for _, neighborID := range h.aoi.VisibleNeighbors(moverID) {
			if _, inOld := oldNeighbors[neighborID]; !inOld {
				continue
			}
			if _, connected := h.clients[neighborID]; !connected {
				continue
			}
			entry := getOrCreateRecipient(byRecipient, neighborID)
			entry.Positions = append(entry.Positions, position)
		}
	}

	// 待处理进入：从进入者扇出到当前可见的已连接邻居
	for _, state := range pendingEntered {
		for _, neighborID := range h.aoi.VisibleNeighbors(state.ID) {
			if _, connected := h.clients[neighborID]; !connected {
				continue
			}
			entry := getOrCreateRecipient(byRecipient, neighborID)
			entry.Entered = append(entry.Entered, state)
		}
	}

	// 待处理离开（已按接收者 key 存储）
	for recipientID, leftIDs := range pendingLeft {
		if _, connected := h.clients[recipientID]; !connected {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.LeftPlayerIDs = append(entry.LeftPlayerIDs, leftIDs...)
	}

	// 待处理外观变更：变更者本人和其可见邻居
	for playerID, appearance := range pendingAppearances {
		if _, connected := h.clients[playerID]; connected {
			entry := getOrCreateRecipient(byRecipient, playerID)
			entry.Appearances = append(entry.Appearances, PlayerAppearanceUpdate{
				PlayerID:   playerID,
				Appearance: appearance,
			})
		}
		for _, neighborID := range h.aoi.VisibleNeighbors(playerID) {
			if _, connected := h.clients[neighborID]; connected {
				entry := getOrCreateRecipient(byRecipient, neighborID)
				entry.Appearances = append(entry.Appearances, PlayerAppearanceUpdate{
					PlayerID:   playerID,
					Appearance: appearance,
				})
			}
		}
	}

	// 编码并只发送给累积了变更的接收者
	for recipientID, changes := range byRecipient {
		client, connected := h.clients[recipientID]
		if !connected {
			continue
		}
		data, ok, err := TryEncodeReplicationUpdate(tick, recipientID, *changes)
		if err != nil {
			log.Printf("encode replication update failed: %v", err)
			continue
		}
		if !ok {
			continue
		}

		h.stats.replicationMessages++
		h.stats.replicationRecipients++
		h.stats.replicationBytes += uint64(len(data))

		if sendOK := client.Send(data); !sendOK {
			h.removeClient(client)
		}
	}
}

// getOrCreateRecipient 在广播本地累积 map 中获取或创建接收者条目。
func getOrCreateRecipient(byRecipient map[int64]*ReplicationChanges, recipientID int64) *ReplicationChanges {
	entry, ok := byRecipient[recipientID]
	if !ok {
		entry = &ReplicationChanges{}
		byRecipient[recipientID] = entry
	}
	return entry
}

// snapshotMoverVisibility 只为移动者捕获旧可见邻居集，不再复制全部已连接客户端。
func (h *Hub) snapshotMoverVisibility(movedIDs []int64) map[int64]map[int64]struct{} {
	snapshot := make(map[int64]map[int64]struct{}, len(movedIDs))
	for _, playerID := range movedIDs {
		neighbors := h.aoi.VisibleNeighbors(playerID)
		set := make(map[int64]struct{}, len(neighbors))
		for _, neighborID := range neighbors {
			set[neighborID] = struct{}{}
		}
		snapshot[playerID] = set
	}
	return snapshot
}

// moverHadNeighbor 检查 observerID 在 mover 移动前是否在 mover 的可见邻居集中。
// 利用 AOI 对称性：旧可见集 key=clientID 含 playerID ⇔ key=playerID 含 clientID。
func (h *Hub) moverHadNeighbor(oldNeighborsByMover map[int64]map[int64]struct{}, moverID, observerID int64) bool {
	neighbors, ok := oldNeighborsByMover[moverID]
	if !ok {
		return false
	}
	_, visible := neighbors[observerID]
	return visible
}

func (h *Hub) applyMovementAOIChanges(movedIDs []int64) {
	for _, playerID := range movedIDs {
		position, ok := h.world.PlayerPosition(playerID)
		if !ok {
			continue
		}
		changes := h.aoi.Move(playerID, position.Lat, position.Lng)
		state, ok := h.world.PlayerState(playerID)
		if !ok {
			continue
		}
		for _, neighborID := range changes.Entered {
			if _, connected := h.clients[neighborID]; !connected {
				continue
			}
			if _, already := h.pendingEntered[playerID]; !already {
				h.pendingEntered[playerID] = state
			}
		}
		for _, neighborID := range changes.Left {
			if _, connected := h.clients[neighborID]; !connected {
				continue
			}
			h.pendingLeft[neighborID] = append(h.pendingLeft[neighborID], playerID)
		}
	}
}

func (h *Hub) takePendingLeft() map[int64][]int64 {
	if len(h.pendingLeft) == 0 {
		return nil
	}
	left := make(map[int64][]int64, len(h.pendingLeft))
	for clientID, playerIDs := range h.pendingLeft {
		left[clientID] = append([]int64(nil), playerIDs...)
	}
	clear(h.pendingLeft)
	return left
}

func (h *Hub) takePendingEntered() []game.PlayerState {
	if len(h.pendingEntered) == 0 {
		return nil
	}
	states := make([]game.PlayerState, 0, len(h.pendingEntered))
	for playerID := range h.pendingEntered {
		if state, ok := h.world.PlayerState(playerID); ok {
			states = append(states, state)
		}
	}
	clear(h.pendingEntered)
	return states
}

func (h *Hub) takePendingAppearances() map[int64]game.Appearance {
	if len(h.pendingAppearances) == 0 {
		return nil
	}
	appearances := make(map[int64]game.Appearance, len(h.pendingAppearances))
	for playerID, appearance := range h.pendingAppearances {
		appearances[playerID] = appearance
	}
	clear(h.pendingAppearances)
	return appearances
}

func (h *Hub) logStats() {
	aoiStats := h.aoi.TakeStats()
	h.stats.aoiCandidatePairs += aoiStats.CandidatePairs
	h.stats.aoiDistanceChecks += aoiStats.DistanceChecks
	h.stats.aoiRelationshipsEntered += aoiStats.RelationshipsEntered
	h.stats.aoiRelationshipsLeft += aoiStats.RelationshipsLeft

	snap := &HubSnapshot{
		ConnectedClients:      len(h.clients),
		AcceptedInputs:        h.stats.acceptedInputs,
		SimulationTicks:       h.stats.simulationTicks,
		MovedPlayers:          h.stats.movedPlayers,
		AOICandidatePairs:     h.stats.aoiCandidatePairs,
		AOIDistanceChecks:     h.stats.aoiDistanceChecks,
		RelationshipsEntered:  h.stats.aoiRelationshipsEntered,
		RelationshipsLeft:     h.stats.aoiRelationshipsLeft,
		ReplicationMessages:   h.stats.replicationMessages,
		ReplicationRecipients: h.stats.replicationRecipients,
		ReplicationBytes:      h.stats.replicationBytes,
		SampledAt:             time.Now(),
	}
	h.snapshot.Store(snap)

	log.Printf(
		"realtime stats clients=%d inputs=%d simulation_ticks=%d moved_players=%d aoi_candidates=%d aoi_distance_checks=%d aoi_entered=%d aoi_left=%d replication_messages=%d replication_recipients=%d replication_bytes=%d",
		snap.ConnectedClients,
		snap.AcceptedInputs,
		snap.SimulationTicks,
		snap.MovedPlayers,
		snap.AOICandidatePairs,
		snap.AOIDistanceChecks,
		snap.RelationshipsEntered,
		snap.RelationshipsLeft,
		snap.ReplicationMessages,
		snap.ReplicationRecipients,
		snap.ReplicationBytes,
	)
	h.stats = intervalStats{}
}
