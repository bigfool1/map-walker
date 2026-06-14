package realtime

import (
	"log"
	"sync"
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

	selfData, err := EncodeSelfState(tick, self)
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

	visibilityBefore := h.snapshotVisibility()
	h.applyMovementAOIChanges(movedIDs)
	h.stats.movedPlayers += uint64(len(movedIDs))

	pendingEntered := h.takePendingEntered()
	pendingLeft := h.takePendingLeft()
	pendingAppearances := h.takePendingAppearances()

	if len(movedIDs) == 0 && len(pendingEntered) == 0 && len(pendingLeft) == 0 && len(pendingAppearances) == 0 {
		return
	}

	tick := h.world.Tick()
	movedSet := int64Set(movedIDs)

	for _, client := range h.clients {
		changes := ReplicationChanges{
			LeftPlayerIDs: pendingLeft[client.ID()],
		}

		if setContains(movedSet, client.ID()) {
			if position, ok := h.world.PlayerPosition(client.ID()); ok {
				changes.SelfPosition = &SelfPosition{Lat: position.Lat, Lng: position.Lng}
			}
		}

		for _, playerID := range movedIDs {
			if playerID == client.ID() {
				continue
			}
			if !h.wasVisibleTo(visibilityBefore, client.ID(), playerID) || !h.isVisibleTo(client.ID(), playerID) {
				continue
			}
			if position, ok := h.world.PlayerPosition(playerID); ok {
				changes.Positions = append(changes.Positions, position)
			}
		}

		for _, state := range pendingEntered {
			if state.ID != client.ID() && h.isVisibleTo(client.ID(), state.ID) {
				changes.Entered = append(changes.Entered, state)
			}
		}

		for playerID, appearance := range pendingAppearances {
			if playerID != client.ID() && !h.isVisibleTo(client.ID(), playerID) {
				continue
			}
			changes.Appearances = append(changes.Appearances, PlayerAppearanceUpdate{
				PlayerID:   playerID,
				Appearance: appearance,
			})
		}

		data, ok, err := TryEncodeReplicationUpdate(tick, client.ID(), changes)
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

func (h *Hub) snapshotVisibility() map[int64]map[int64]struct{} {
	snapshot := make(map[int64]map[int64]struct{}, len(h.clients))
	for clientID := range h.clients {
		neighbors := h.aoi.VisibleNeighbors(clientID)
		set := make(map[int64]struct{}, len(neighbors))
		for _, neighborID := range neighbors {
			set[neighborID] = struct{}{}
		}
		snapshot[clientID] = set
	}
	return snapshot
}

func (h *Hub) wasVisibleTo(visibilityBefore map[int64]map[int64]struct{}, clientID, playerID int64) bool {
	neighbors, ok := visibilityBefore[clientID]
	if !ok {
		return false
	}
	_, visible := neighbors[playerID]
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

	log.Printf(
		"realtime stats clients=%d inputs=%d simulation_ticks=%d moved_players=%d aoi_candidates=%d aoi_distance_checks=%d aoi_entered=%d aoi_left=%d replication_messages=%d replication_recipients=%d replication_bytes=%d",
		len(h.clients),
		h.stats.acceptedInputs,
		h.stats.simulationTicks,
		h.stats.movedPlayers,
		h.stats.aoiCandidatePairs,
		h.stats.aoiDistanceChecks,
		h.stats.aoiRelationshipsEntered,
		h.stats.aoiRelationshipsLeft,
		h.stats.replicationMessages,
		h.stats.replicationRecipients,
		h.stats.replicationBytes,
	)
	h.stats = intervalStats{}
}
