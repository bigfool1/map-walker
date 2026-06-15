package tester_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"map-walker/internal/realtime"
	"map-walker/internal/tester"
)

func TestClientReadCountsMessages(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := context.Background()
		conn.Write(ctx, websocket.MessageText, []byte("a"))
		conn.Write(ctx, websocket.MessageText, []byte("b"))
		conn.Write(ctx, websocket.MessageText, []byte("c"))
		conn.Close(websocket.StatusNormalClosure, "done")
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := tester.NewClient(conn)
	c.Run(context.Background())
	c.WaitDone()

	if got := c.MessagesRead(); got != 3 {
		t.Errorf("MessagesRead = %d, want 3", got)
	}
}

func TestClientSendWritesJSON(t *testing.T) {
	received := make(chan []byte, 1)

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_, data, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		received <- data
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := tester.NewClient(conn)

	input := realtime.InputMessage{
		Type: realtime.MessageTypeInput,
		Up:   true,
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	if ok := c.Send(data); !ok {
		t.Fatal("Send returned false")
	}

	go c.Run(context.Background())

	select {
	case got := <-received:
		var parsed realtime.InputMessage
		if err := json.Unmarshal(got, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed != input {
			t.Errorf("got %+v, want %+v", parsed, input)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}

	c.Close()
}
