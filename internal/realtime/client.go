package realtime

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const DefaultSendBufferSize = 16

// pingIntervalNs and pingTimeoutNs store durations as nanoseconds so tests can
// safely override them from a different goroutine without a data race.
var (
	pingIntervalNs atomic.Int64
	pingTimeoutNs  atomic.Int64
)

func init() {
	pingIntervalNs.Store(int64(15 * time.Second))
	pingTimeoutNs.Store(int64(5 * time.Second))
}

type Client struct {
	id          int64
	username    string
	conn        *websocket.Conn
	hub         *Hub
	isSynthetic bool
	send        chan []byte
	cancel      context.CancelFunc
	closeOnce   sync.Once
}

func NewClient(id int64, username string, conn *websocket.Conn, hub *Hub) *Client {
	return &Client{
		id:       id,
		username: username,
		conn:     conn,
		hub:      hub,
		send:     make(chan []byte, DefaultSendBufferSize),
	}
}

// NewClientWithSynthetic 创建带合成身份的客户端
func NewClientWithSynthetic(id int64, username string, conn *websocket.Conn, hub *Hub, isSynthetic bool) *Client {
	c := NewClient(id, username, conn, hub)
	c.isSynthetic = isSynthetic
	return c
}

// IsSynthetic 返回客户端的服务端信任合成身份
func (c *Client) IsSynthetic() bool {
	return c.isSynthetic
}

func (c *Client) ID() int64 {
	return c.id
}

func (c *Client) Username() string {
	return c.username
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

		// 尝试解析 input 消息
		var inputMsg InputMessage
		if err := json.Unmarshal(data, &inputMsg); err != nil {
			log.Printf("decode websocket message failed: %v", err)
			continue
		}
		if inputMsg.Type == MessageTypeInput {
			if ok := c.hub.ApplyInput(c, inputMsg.InputState()); !ok {
				return
			}
			continue
		}

		// 尝试解析 collect 消息
		var collectMsg CollectMessage
		if err := json.Unmarshal(data, &collectMsg); err != nil {
			log.Printf("decode websocket message failed: %v", err)
			continue
		}
		if collectMsg.Type == MessageTypeCollect {
			c.hub.SubmitCollect(c, collectMsg.CollectibleID)
			continue
		}
	}
}

func (c *Client) writeLoop(ctx context.Context) {
	// WebSocket writes are kept in one goroutine. This mirrors the common Python
	// rule of making one task responsible for socket writes so concurrent sends
	// do not interleave frames or fight over connection state.
	for data := range c.send {
		writeCtx, cancel := context.WithTimeout(ctx, time.Duration(pingTimeoutNs.Load()))
		err := c.conn.Write(writeCtx, websocket.MessageText, data)
		cancel()
		if err != nil {
			c.finish()
			return
		}
	}
}

func (c *Client) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(pingIntervalNs.Load()))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, time.Duration(pingTimeoutNs.Load()))
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				c.finish()
				return
			}
		}
	}
}
