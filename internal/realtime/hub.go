package realtime

import (
	"encoding/json"

	"map-walker/internal/game"
)

type ClientSender interface {
	ID() string
	Send([]byte) bool
	CloseSend()
}

type Hub struct {
	state      *game.State
	register   chan ClientSender
	unregister chan ClientSender
	updates    chan PositionUpdateMessage
	stop       chan struct{}
	clients    map[string]ClientSender
}

func NewHub() *Hub {
	return &Hub{
		state:      game.NewState(),
		register:   make(chan ClientSender),
		unregister: make(chan ClientSender),
		updates:    make(chan PositionUpdateMessage),
		stop:       make(chan struct{}),
		clients:    map[string]ClientSender{},
	}
}

// Run is the backend's tiny "world loop".
//
// Python comparison: this looks like one long-running asyncio task that owns an
// asyncio.Queue. Other goroutines send events into channels; this goroutine is
// the only place that mutates clients and player state, so we avoid sprinkling
// locks through the rest of the code.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client.ID()] = client
			h.sendSnapshot(client)
		case client := <-h.unregister:
			h.removeClient(client)
			h.broadcastSnapshot()
		case update := <-h.updates:
			h.state.UpdatePosition(game.PlayerPosition{
				ID:  update.PlayerID,
				Lat: update.Lat,
				Lng: update.Lng,
			})
			h.broadcastSnapshot()
		case <-h.stop:
			for _, client := range h.clients {
				client.CloseSend()
			}
			return
		}
	}
}

func (h *Hub) Stop() {
	close(h.stop)
}

func (h *Hub) Register(client ClientSender) {
	h.register <- client
}

func (h *Hub) Unregister(client ClientSender) {
	h.unregister <- client
}

func (h *Hub) UpdatePosition(update PositionUpdateMessage) {
	h.updates <- update
}

func (h *Hub) removeClient(client ClientSender) {
	if _, ok := h.clients[client.ID()]; !ok {
		return
	}
	delete(h.clients, client.ID())
	h.state.RemovePlayer(client.ID())
	client.CloseSend()
}

func (h *Hub) sendSnapshot(client ClientSender) {
	data := h.snapshotData()
	if ok := client.Send(data); !ok {
		h.removeClient(client)
	}
}

func (h *Hub) broadcastSnapshot() {
	data := h.snapshotData()
	for _, client := range h.clients {
		if ok := client.Send(data); !ok {
			h.removeClient(client)
		}
	}
}

func (h *Hub) snapshotData() []byte {
	msg := NewPlayersSnapshotMessage(h.state.Snapshot())
	data, err := json.Marshal(msg)
	if err != nil {
		panic(err)
	}
	return data
}
