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

type SavedPositionLoader func(userID string) (lat, lng float64, ok bool)

type Hub struct {
	world             *game.World
	loadSavedPosition SavedPositionLoader
	persister         PositionPersister
	register          chan ClientSender
	unregister        chan ClientSender
	inputs            chan inputEvent
	stop              chan struct{}
	done              chan struct{}
	stopOnce          sync.Once
	clients           map[string]ClientSender
	persistDirty      map[string]struct{}
	persistSeq        map[string]uint64
	disconnectUser    chan disconnectRequest
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

func NewHubWithSavedPositions(loader SavedPositionLoader, persister PositionPersister) *Hub {
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
	loadSavedPosition SavedPositionLoader,
	persister PositionPersister,
	simulationTick <-chan time.Time,
	broadcastTick <-chan time.Time,
	persistenceTick <-chan time.Time,
	statsTick <-chan time.Time,
	stopTickers func(),
) *Hub {
	return &Hub{
		world:             world,
		loadSavedPosition: loadSavedPosition,
		persister:         persister,
		register:          make(chan ClientSender),
		unregister:        make(chan ClientSender),
		inputs:            make(chan inputEvent),
		stop:              make(chan struct{}),
		done:              make(chan struct{}),
		clients:           map[string]ClientSender{},
		persistDirty:      map[string]struct{}{},
		persistSeq:        map[string]uint64{},
		disconnectUser:    make(chan disconnectRequest),
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
			h.broadcastDelta()
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

func (h *Hub) registerClient(client ClientSender) {
	if existing, exists := h.clients[client.ID()]; exists && existing != client {
		existing.CloseSend()
		h.world.ResetInput(client.ID())
	} else if !h.world.HasPlayer(client.ID()) {
		h.addPlayer(client.ID())
	}

	h.clients[client.ID()] = client
	h.sendSnapshot(client)
}

func (h *Hub) addPlayer(userID string) {
	if h.loadSavedPosition != nil {
		if lat, lng, ok := h.loadSavedPosition(userID); ok {
			h.world.AddPlayerAt(userID, lat, lng)
			return
		}
	}
	h.world.AddPlayer(userID)
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

func (h *Hub) sendSnapshot(client ClientSender) {
	data, err := EncodeWorldSnapshot(h.world.Snapshot())
	if err != nil {
		log.Printf("encode world snapshot failed: %v", err)
		h.removeClient(client)
		return
	}
	if ok := client.Send(data); !ok {
		h.removeClient(client)
	}
}

func (h *Hub) broadcastDelta() {
	delta := h.world.TakeDelta()
	if !delta.HasChanges() {
		return
	}

	data, err := EncodePlayersDelta(delta)
	if err != nil {
		log.Printf("encode players delta failed: %v", err)
		return
	}

	h.stats.deltaBroadcasts++
	h.stats.changedPlayers += uint64(len(delta.Players))
	h.stats.deltaBytes += uint64(len(data))

	for _, client := range h.clients {
		if ok := client.Send(data); !ok {
			h.removeClient(client)
		}
	}
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
