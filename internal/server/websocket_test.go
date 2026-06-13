package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func newRunningTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := newTestServer(t)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)
	return server
}

func TestWebSocketRejectsUnauthenticated(t *testing.T) {
	server := newRunningTestServer(t)
	wsURL := websocketURL(server.URL)

	_, resp, err := websocket.Dial(context.Background(), wsURL, nil)
	if err == nil {
		t.Fatal("expected unauthenticated dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got resp=%+v err=%v", resp, err)
	}
}

func TestWebSocketAcceptsAuthenticatedSession(t *testing.T) {
	server := newRunningTestServer(t)

	register := postJSON(t, server.URL+"/api/register", `{"username":"Alice","password":"password123"}`)
	cookie := findSessionCookie(t, register.Cookies())
	register.Body.Close()

	conn, resp, err := dialWebSocket(server.URL, cookie)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.CloseNow()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("read snapshot failed: %v", err)
	}

	var message struct {
		Type   string `json:"type"`
		Player struct {
			ID string `json:"id"`
		} `json:"player"`
	}
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode self state failed: %v", err)
	}
	if message.Type != "self_state" || message.Player.ID == "" {
		t.Fatalf("unexpected self state: %s", data)
	}
}

func TestWebSocketIgnoresClientSuppliedPlayerID(t *testing.T) {
	server := newRunningTestServer(t)

	register := postJSON(t, server.URL+"/api/register", `{"username":"Alice","password":"password123"}`)
	cookie := findSessionCookie(t, register.Cookies())
	register.Body.Close()

	session := getSession(t, server.URL, cookie)

	wsURL := websocketURL(server.URL) + "?playerId=attacker-id"
	conn, _, err := dialWebSocketURL(wsURL, cookie)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.CloseNow()

	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("read snapshot failed: %v", err)
	}

	var message struct {
		Type   string `json:"type"`
		Player struct {
			ID string `json:"id"`
		} `json:"player"`
	}
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode self state failed: %v", err)
	}
	if message.Type != "self_state" || message.Player.ID != session.UserID {
		t.Fatalf("expected authenticated user id %q, got %+v", session.UserID, message)
	}
	if message.Player.ID == "attacker-id" {
		t.Fatal("client-supplied playerId must not be used")
	}
}

func websocketURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + "/ws"
}

func dialWebSocket(httpURL string, cookie *http.Cookie) (*websocket.Conn, *http.Response, error) {
	return dialWebSocketURL(websocketURL(httpURL), cookie)
}

func dialWebSocketURL(wsURL string, cookie *http.Cookie) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	header.Add("Cookie", cookie.String())
	return websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: header,
	})
}

func getSession(t *testing.T, baseURL string, cookie *http.Cookie) sessionResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/session", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("session request failed: %v", err)
	}
	defer resp.Body.Close()

	return decodeSessionResponse(t, resp.Body)
}
