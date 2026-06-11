package realtime

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const sendBufferSize = 16

type Client struct {
	id        string
	conn      *websocket.Conn
	hub       *Hub
	send      chan []byte
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func NewClient(id string, conn *websocket.Conn, hub *Hub) *Client {
	return &Client{
		id:   id,
		conn: conn,
		hub:  hub,
		send: make(chan []byte, sendBufferSize),
	}
}

func (c *Client) ID() string {
	return c.id
}

func (c *Client) Send(data []byte) bool {
	select {
	case c.send <- data:
		return true
	default:
		// Backpressure lesson: this is like an asyncio.Queue with maxsize.
		// If the browser cannot drain messages, we prefer dropping the
		// connection over letting memory grow forever.
		return false
	}
}

func (c *Client) CloseSend() {
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		close(c.send)
		_ = c.conn.Close(websocket.StatusGoingAway, "disconnected")
	})
}

func (c *Client) Run(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	c.hub.Register(c)
	defer c.hub.Unregister(c)

	go c.writeLoop(ctx)
	c.readLoop(ctx)
}

func (c *Client) readLoop(ctx context.Context) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}

		var msg PositionUpdateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("decode websocket message failed: %v", err)
			continue
		}
		if msg.Type != MessageTypePositionUpdate {
			continue
		}

		msg.PlayerID = c.id
		c.hub.UpdatePosition(msg)
	}
}

func (c *Client) writeLoop(ctx context.Context) {
	// WebSocket writes are kept in one goroutine. This mirrors the common Python
	// rule of making one task responsible for socket writes so concurrent sends
	// do not interleave frames or fight over connection state.
	for data := range c.send {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.conn.Write(writeCtx, websocket.MessageText, data)
		cancel()
		if err != nil {
			return
		}
	}
}
