package realtime

import (
	"log"
	"sync"
	"time"

	"map-walker/internal/game"
)

const (
	simulationInterval   = 50 * time.Millisecond
	broadcastInterval    = 100 * time.Millisecond
	persistenceInterval  = 5 * time.Second
	statsInterval        = time.Second
)

type ClientSender interface {
	ID() string
	Username() string
	Send([]byte) bool
	CloseSend()
}

type disconnectRequest struct {
	userID string
	done   chan struct{}
}

type inputEvent struct {
	client ClientSender
	input  game.InputState
}

type appearanceUpdateRequest struct {
	userID     string
	appearance game.Appearance
	done       chan struct{}
}

type Hub struct {
	world              *game.World
	loadSavedPlayer    SavedPlayerLoader
	persister          PositionPersister
	register           chan ClientSender
	unregister         chan ClientSender
	inputs             chan inputEvent
	appearanceUpdates  chan appearanceUpdateRequest
	stop               chan struct{}
	done               chan struct{}
	stopOnce           sync.Once
	clients            map[string]ClientSender
	persistDirty       map[string]struct{}
	persistSeq         map[string]uint64
	pendingEntered     map[string]game.PlayerState
	pendingAppearances map[string]game.Appearance
	disconnectUser     chan disconnectRequest
	simulationTick    <-chan time.Time
	broadcastTick     <-chan time.Time
	persistenceTick   <-chan time.Time
	statsTick         <-chan time.Time
	stopTickers       func()
	stats             intervalStats
}

type intervalStats struct {
	acceptedInputs  uint64
	simulationTicks uint64
	deltaBroadcasts uint64
	changedPlayers  uint64
	deltaBytes      uint64
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
		world:             world,
		loadSavedPlayer:   loadSavedPlayer,
		persister:         persister,
		register:          make(chan ClientSender),
		unregister:        make(chan ClientSender),
		inputs:            make(chan inputEvent),
		appearanceUpdates: make(chan appearanceUpdateRequest),
		stop:              make(chan struct{}),
		done:              make(chan struct{}),
		clients:            map[string]ClientSender{},
		persistDirty:       map[string]struct{}{},
		persistSeq:         map[string]uint64{},
		pendingEntered:     map[string]game.PlayerState{},
		pendingAppearances: map[string]game.Appearance{},
		disconnectUser:     make(chan disconnectRequest),
		simulationTick:    simulationTick,
		broadcastTick:     broadcastTick,
		persistenceTick:   persistenceTick,
		statsTick:         statsTick,
		stopTickers:       stopTickers,
	}
}

// Run is the single owner of both connections and authoritative world state.
//
// Python comparison: this is one long-running asyncio task selecting between
// queue events and timer events. World itself stays synchronous and
// deterministic; only this orchestration layer knows about concurrency.
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

func (h *Hub) DisconnectUser(userID string) {
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

func (h *Hub) UpdateAppearance(userID string, appearance game.Appearance) bool {
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
	} else if !h.world.HasPlayer(client.ID()) {
		h.addPlayer(client.ID(), client.Username())
		if len(h.clients) > 0 {
			if state, ok := h.world.PlayerState(client.ID()); ok {
				h.pendingEntered[client.ID()] = state
			}
		}
	}

	h.clients[client.ID()] = client
	h.sendInitialization(client)
}

func (h *Hub) addPlayer(userID, username string) {
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
	h.world.RemovePlayer(client.ID())
	delete(h.persistDirty, client.ID())
	delete(h.persistSeq, client.ID())
	delete(h.pendingEntered, client.ID())
	delete(h.pendingAppearances, client.ID())
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

func (h *Hub) submitFinalPosition(userID string) {
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

	visibleIDs := make([]string, 0, len(h.world.PlayerIDs()))
	for _, playerID := range h.world.PlayerIDs() {
		if playerID != client.ID() {
			visibleIDs = append(visibleIDs, playerID)
		}
	}
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
	removedIDs := h.world.TakeRemovedPlayerIDs()
	pendingEntered := h.takePendingEntered()
	pendingAppearances := h.takePendingAppearances()

	if len(movedIDs) == 0 && len(removedIDs) == 0 && len(pendingEntered) == 0 && len(pendingAppearances) == 0 {
		return
	}

	tick := h.world.Tick()
	movedSet := stringSet(movedIDs)

	for _, client := range h.clients {
		changes := ReplicationChanges{
			LeftPlayerIDs: removedIDs,
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
			if position, ok := h.world.PlayerPosition(playerID); ok {
				changes.Positions = append(changes.Positions, position)
			}
		}

		for _, state := range pendingEntered {
			if state.ID != client.ID() {
				changes.Entered = append(changes.Entered, state)
			}
		}

		for playerID, appearance := range pendingAppearances {
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

		h.stats.deltaBroadcasts++
		h.stats.changedPlayers++
		h.stats.deltaBytes += uint64(len(data))

		if sendOK := client.Send(data); !sendOK {
			h.removeClient(client)
		}
	}
}

func (h *Hub) takePendingEntered() []game.PlayerState {
	if len(h.pendingEntered) == 0 {
		return nil
	}
	states := make([]game.PlayerState, 0, len(h.pendingEntered))
	for _, state := range h.pendingEntered {
		states = append(states, state)
	}
	clear(h.pendingEntered)
	return states
}

func (h *Hub) takePendingAppearances() map[string]game.Appearance {
	if len(h.pendingAppearances) == 0 {
		return nil
	}
	appearances := make(map[string]game.Appearance, len(h.pendingAppearances))
	for playerID, appearance := range h.pendingAppearances {
		appearances[playerID] = appearance
	}
	clear(h.pendingAppearances)
	return appearances
}

func (h *Hub) logStats() {
	log.Printf(
		"realtime stats clients=%d inputs=%d simulation_ticks=%d delta_broadcasts=%d changed_players=%d delta_bytes=%d",
		len(h.clients),
		h.stats.acceptedInputs,
		h.stats.simulationTicks,
		h.stats.deltaBroadcasts,
		h.stats.changedPlayers,
		h.stats.deltaBytes,
	)
	h.stats = intervalStats{}
}
