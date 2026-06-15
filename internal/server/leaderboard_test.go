package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func registerUserCookie(t *testing.T, baseURL, username, password string) *http.Cookie {
	t.Helper()
	body := `{"username":"` + username + `","password":"` + password + `"}`
	resp := postJSON(t, baseURL+"/api/register", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register %s failed: %d", username, resp.StatusCode)
	}
	return findSessionCookie(t, resp.Cookies())
}

func getWithCookie(t *testing.T, url string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func TestLeaderboardRequiresAuth(t *testing.T) {
	server := newRunningTestServer(t)

	resp := getWithCookie(t, server.URL+"/api/leaderboard/online", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLeaderboardRejectsPost(t *testing.T) {
	server := newRunningTestServer(t)
	cookie := registerCookie(t, server.URL)

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/leaderboard/online", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestLeaderboardReturnsTopAndSelf(t *testing.T) {
	server := newRunningTestServer(t)
	cookie := registerCookie(t, server.URL)

	conn, _, err := dialWebSocket(server.URL, cookie)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.CloseNow()

	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read init msg %d failed: %v", i+1, err)
		}
	}

	resp := getWithCookie(t, server.URL+"/api/leaderboard/online", cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Top  []leaderboardEntryResponse `json:"top"`
		Self *struct {
			Score int64 `json:"score"`
			Rank  int   `json:"rank"`
		} `json:"self"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(body.Top) < 1 {
		t.Fatal("expected at least 1 top entry")
	}
	if body.Self == nil {
		t.Fatal("connected user should have self entry")
	}
}

func TestLeaderboardOmitsSelfWhenOffline(t *testing.T) {
	server := newRunningTestServer(t)
	cookie := registerCookie(t, server.URL)

	resp := getWithCookie(t, server.URL+"/api/leaderboard/online", cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Self *struct {
			Score int64 `json:"score"`
			Rank  int   `json:"rank"`
		} `json:"self"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body.Self != nil {
		t.Fatal("offline user should not have self entry")
	}
}

func TestLeaderboardTopOrderedByScoreDesc(t *testing.T) {
	server := newRunningTestServer(t)

	type player struct {
		cookie *http.Cookie
		conn   *websocket.Conn
	}
	var players []player
	for _, name := range []string{"LB1", "LB2", "LB3"} {
		c := registerUserCookie(t, server.URL, name, "password123")
		conn, _, err := dialWebSocket(server.URL, c)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		players = append(players, player{cookie: c, conn: conn})
		for i := 0; i < 4; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				t.Fatalf("read init %d failed: %v", i+1, err)
			}
		}
	}
	defer func() {
		for _, p := range players {
			p.conn.CloseNow()
		}
	}()

	resp := getWithCookie(t, server.URL+"/api/leaderboard/online", players[0].cookie)
	defer resp.Body.Close()

	var body struct {
		Top  []leaderboardEntryResponse `json:"top"`
		Self *struct {
			Rank  int   `json:"rank"`
			Score int64 `json:"score"`
		} `json:"self"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(body.Top) != 3 {
		t.Fatalf("expected 3 top entries, got %d", len(body.Top))
	}
	for i := 1; i < len(body.Top); i++ {
		if body.Top[i].Score > body.Top[i-1].Score {
			t.Fatalf("top[%d].score=%d > top[%d].score=%d", i, body.Top[i].Score, i-1, body.Top[i-1].Score)
		}
	}
	if body.Self == nil {
		t.Fatal("Alice should have self entry")
	}
}

type leaderboardEntryResponse struct {
	PlayerID int64  `json:"playerId"`
	Username string `json:"username"`
	Score    int64  `json:"score"`
}
