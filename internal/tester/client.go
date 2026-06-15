package tester

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

const sendBufferSize = 64

type Client struct {
	conn *websocket.Conn
	send chan []byte
	done chan struct{}

	closeOnce sync.Once

	messagesRead atomic.Uint64
	bytesRead    atomic.Uint64
	writeFails   atomic.Uint64
}

func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		conn: conn,
		send: make(chan []byte, sendBufferSize),
		done: make(chan struct{}),
	}
}

func (c *Client) Send(data []byte) bool {
	select {
	case c.send <- data:
		return true
	default:
		return false
	}
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.send)
		c.conn.Close(websocket.StatusNormalClosure, "")
	})
}

func (c *Client) WaitDone() {
	<-c.done
}

func (c *Client) MessagesRead() uint64 {
	return c.messagesRead.Load()
}

func (c *Client) BytesRead() uint64 {
	return c.bytesRead.Load()
}

func (c *Client) WriteFails() uint64 {
	return c.writeFails.Load()
}

func (c *Client) readLoop(ctx context.Context) {
	defer close(c.done)
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		c.messagesRead.Add(1)
		c.bytesRead.Add(uint64(len(data)))
	}
}

func (c *Client) writeLoop(ctx context.Context) {
	for data := range c.send {
		err := c.conn.Write(ctx, websocket.MessageText, data)
		if err != nil {
			c.writeFails.Add(1)
			return
		}
	}
}

func (c *Client) Run(ctx context.Context) {
	go c.writeLoop(ctx)
	c.readLoop(ctx)
}
