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

var (
	pingInterval = 15 * time.Second
	pingTimeout  = 5 * time.Second
)

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
	c.finish()
}

func (c *Client) finish() {
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
	if ok := c.hub.Register(c); !ok {
		c.finish()
		return
	}
	defer func() {
		c.finish()
		c.hub.Unregister(c)
	}()

	go c.writeLoop(ctx)
	go c.heartbeatLoop(ctx)
	c.readLoop(ctx)
}

func (c *Client) readLoop(ctx context.Context) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}

		var message InputMessage
		if err := json.Unmarshal(data, &message); err != nil {
			log.Printf("decode websocket message failed: %v", err)
			continue
		}
		if message.Type != MessageTypeInput {
			continue
		}

		if ok := c.hub.ApplyInput(c, message.InputState()); !ok {
			return
		}
	}
}

func (c *Client) writeLoop(ctx context.Context) {
	// WebSocket writes are kept in one goroutine. This mirrors the common Python
	// rule of making one task responsible for socket writes so concurrent sends
	// do not interleave frames or fight over connection state.
	for data := range c.send {
		writeCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		err := c.conn.Write(writeCtx, websocket.MessageText, data)
		cancel()
		if err != nil {
			c.finish()
			return
		}
	}
}

func (c *Client) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				c.finish()
				return
			}
		}
	}
}
