package realtime

import (
	"log"
	"sync"
	"time"

	"map-walker/internal/game"
)

const (
	simulationInterval = 50 * time.Millisecond
	broadcastInterval  = 100 * time.Millisecond
	statsInterval      = time.Second
)

type ClientSender interface {
	ID() string
	Send([]byte) bool
	CloseSend()
}

type inputEvent struct {
	client ClientSender
	input  game.InputState
}

type SavedPositionLoader func(userID string) (lat, lng float64, ok bool)

type Hub struct {
	world              *game.World
	loadSavedPosition  SavedPositionLoader
	register           chan ClientSender
	unregister     chan ClientSender
	inputs         chan inputEvent
	stop           chan struct{}
	done           chan struct{}
	stopOnce       sync.Once
	clients        map[string]ClientSender
	simulationTick <-chan time.Time
	broadcastTick  <-chan time.Time
	statsTick      <-chan time.Time
	stopTickers    func()
	stats          intervalStats
}

type intervalStats struct {
	acceptedInputs  uint64
	simulationTicks uint64
	deltaBroadcasts uint64
	changedPlayers  uint64
	deltaBytes      uint64
}

func NewHub() *Hub {
	return NewHubWithSavedPositions(nil)
}

func NewHubWithSavedPositions(loader SavedPositionLoader) *Hub {
	simulationTicker := time.NewTicker(simulationInterval)
	broadcastTicker := time.NewTicker(broadcastInterval)
	statsTicker := time.NewTicker(statsInterval)

	return newHub(
		game.NewWorld(game.DefaultConfig()),
		loader,
		simulationTicker.C,
		broadcastTicker.C,
		statsTicker.C,
		func() {
			simulationTicker.Stop()
			broadcastTicker.Stop()
			statsTicker.Stop()
		},
	)
}

func newHub(
	world *game.World,
	loadSavedPosition SavedPositionLoader,
	simulationTick <-chan time.Time,
	broadcastTick <-chan time.Time,
	statsTick <-chan time.Time,
	stopTickers func(),
) *Hub {
	return &Hub{
		world:             world,
		loadSavedPosition: loadSavedPosition,
		register:          make(chan ClientSender),
		unregister:     make(chan ClientSender),
		inputs:         make(chan inputEvent),
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
		clients:        map[string]ClientSender{},
		simulationTick: simulationTick,
		broadcastTick:  broadcastTick,
		statsTick:      statsTick,
		stopTickers:    stopTickers,
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
			h.world.Step(simulationInterval)
			h.stats.simulationTicks++
		case <-h.broadcastTick:
			h.broadcastDelta()
		case <-h.statsTick:
			h.logStats()
		case <-h.stop:
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
	h.world.RemovePlayer(client.ID())
	client.CloseSend()
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
