package synthetic

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"map-walker/internal/realtime"
)

var ErrClosedBeforeReady = errors.New("synthetic client closed before readiness")

const initializationMessagesRequired = 2

type Client struct {
	id       int64
	username string
	send     chan []byte

	closeOnce sync.Once
	readyOnce sync.Once
	ready     chan struct{}
	done      chan struct{}

	messagesDrained atomic.Uint64
	bytesDrained    atomic.Uint64
	queueHighWater  atomic.Uint32

	drainDelay time.Duration
}

func NewClient(userID int64, username string) *Client {
	client := &Client{
		id:       userID,
		username: username,
		send:     make(chan []byte, realtime.DefaultSendBufferSize),
		ready:    make(chan struct{}),
		done:     make(chan struct{}),
	}
	go client.drainLoop()
	return client
}

var _ realtime.ClientSender = (*Client)(nil)

func (c *Client) ID() int64 {
	return c.id
}

func (c *Client) Username() string {
	return c.username
}

func (c *Client) Send(data []byte) bool {
	select {
	case c.send <- data:
		c.trackQueueHighWater()
		return true
	default:
		return false
	}
}

func (c *Client) CloseSend() {
	c.closeOnce.Do(func() {
		close(c.send)
	})
}

func (c *Client) Ready() <-chan struct{} {
	return c.ready
}

func (c *Client) Done() <-chan struct{} {
	return c.done
}

func (c *Client) WaitReady(ctx context.Context) error {
	select {
	case <-c.ready:
		return nil
	case <-c.done:
		return ErrClosedBeforeReady
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) WaitDone(ctx context.Context) error {
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) MessagesDrained() uint64 {
	return c.messagesDrained.Load()
}

func (c *Client) BytesDrained() uint64 {
	return c.bytesDrained.Load()
}

func (c *Client) QueueHighWater() uint32 {
	return c.queueHighWater.Load()
}

func (c *Client) trackQueueHighWater() {
	depth := uint32(len(c.send))
	for {
		current := c.queueHighWater.Load()
		if depth <= current {
			return
		}
		if c.queueHighWater.CompareAndSwap(current, depth) {
			return
		}
	}
}

func (c *Client) drainLoop() {
	defer close(c.done)

	var drained int
	for data := range c.send {
		if c.drainDelay > 0 {
			time.Sleep(c.drainDelay)
		}
		c.messagesDrained.Add(1)
		c.bytesDrained.Add(uint64(len(data)))
		drained++
		if drained == initializationMessagesRequired {
			c.readyOnce.Do(func() {
				close(c.ready)
			})
		}
	}
}
