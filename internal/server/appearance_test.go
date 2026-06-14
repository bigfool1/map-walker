package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"map-walker/internal/auth"
	"map-walker/internal/realtime"
	"map-walker/internal/storage"
)

func TestPutAppearanceRequiresAuthentication(t *testing.T) {
	server := newRunningTestServer(t)

	resp := putJSON(t, server.URL+"/api/appearance", `{"color":"#ff6600","shape":"diamond"}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestPutAppearanceRejectsValidationFailures(t *testing.T) {
	server := newRunningTestServer(t)
	cookie := registerCookie(t, server.URL)

	cases := []string{
		`{"shape":"diamond"}`,
		`{"color":"#ff6600"}`,
		`not-json`,
		`{"color":"ff6600","shape":"diamond"}`,
		`{"color":"#ff6600","shape":"hexagon"}`,
	}
	for _, body := range cases {
		resp := putJSON(t, server.URL+"/api/appearance", body, cookie)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s: expected 400, got %d", body, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestPutAppearanceNormalizesAndReturnsAuthoritativeAppearance(t *testing.T) {
	server := newRunningTestServer(t)
	cookie := registerCookie(t, server.URL)

	resp := putJSON(t, server.URL+"/api/appearance", `{"color":"#FF6600","shape":"diamond"}`, cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := decodeAppearanceResponse(t, resp.Body)
	if body.Color != "#ff6600" || body.Shape != "diamond" {
		t.Fatalf("unexpected response: %+v", body)
	}

	session := getSession(t, server.URL, cookie)
	if session.Appearance != body {
		t.Fatalf("session appearance = %+v, want %+v", session.Appearance, body)
	}
}

func TestPutAppearanceOfflineUserSucceeds(t *testing.T) {
	server := newRunningTestServer(t)
	cookie := registerCookie(t, server.URL)

	resp := putJSON(t, server.URL+"/api/appearance", `{"color":"#ff6600","shape":"triangle"}`, cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestPutAppearanceStoppedHubReturns503AndKeepsDatabaseValue(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	hub := realtime.NewHub()
	go hub.Run()
	hub.Stop()

	srv := New(hub, auth.NewService(db))
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	cookie := registerCookie(t, server.URL)

	resp := putJSON(t, server.URL+"/api/appearance", `{"color":"#ff6600","shape":"square"}`, cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}

	session := getSession(t, server.URL, cookie)
	if session.Appearance.Color != "#ff6600" || session.Appearance.Shape != "square" {
		t.Fatalf("expected saved appearance in session, got %+v", session.Appearance)
	}
}

func TestPutAppearanceOnlineUserBroadcastsReplicationAppearance(t *testing.T) {
	server := newRunningTestServer(t)
	cookie := registerCookie(t, server.URL)

	conn, _, err := dialWebSocket(server.URL, cookie)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.CloseNow()

	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := conn.Read(readCtx); err != nil {
		t.Fatalf("read self state failed: %v", err)
	}
	readCtx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := conn.Read(readCtx); err != nil {
		t.Fatalf("read visible entities snapshot failed: %v", err)
	}

	resp := putJSON(t, server.URL+"/api/appearance", `{"color":"#ff6600","shape":"diamond"}`, cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	readCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("read replication update failed: %v", err)
	}

	var message struct {
		Type        string `json:"type"`
		Appearances []struct {
			PlayerID   int64              `json:"playerId"`
			Appearance appearanceResponse `json:"appearance"`
		} `json:"appearances"`
	}
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode replication update failed: %v", err)
	}
	if message.Type != "replication_update" {
		t.Fatalf("expected replication_update, got %q", message.Type)
	}
	if len(message.Appearances) != 1 {
		t.Fatalf("expected one appearance update, got %+v", message)
	}
	if message.Appearances[0].Appearance.Color != "#ff6600" || message.Appearances[0].Appearance.Shape != "diamond" {
		t.Fatalf("unexpected appearance message: %+v", message)
	}
}

func TestRegisterLoginAndSessionIncludeAppearance(t *testing.T) {
	server := newRunningTestServer(t)

	register := postJSON(t, server.URL+"/api/register", `{"username":"Alice","password":"password123"}`)
	defer register.Body.Close()
	if register.StatusCode != http.StatusOK {
		t.Fatalf("register failed: %d", register.StatusCode)
	}
	registerBody := decodeSessionResponse(t, register.Body)
	if registerBody.Appearance.Color != storage.DefaultAppearanceColor || registerBody.Appearance.Shape != storage.DefaultAppearanceShape {
		t.Fatalf("unexpected register appearance: %+v", registerBody.Appearance)
	}

	login := postJSON(t, server.URL+"/api/login", `{"username":"alice","password":"password123"}`)
	defer login.Body.Close()
	loginBody := decodeSessionResponse(t, login.Body)
	if loginBody.Appearance != registerBody.Appearance {
		t.Fatalf("login appearance = %+v, want %+v", loginBody.Appearance, registerBody.Appearance)
	}

	cookie := findSessionCookie(t, login.Cookies())
	session := getSession(t, server.URL, cookie)
	if session.Appearance != registerBody.Appearance {
		t.Fatalf("session appearance = %+v, want %+v", session.Appearance, registerBody.Appearance)
	}
}

func registerCookie(t *testing.T, baseURL string) *http.Cookie {
	t.Helper()
	resp := postJSON(t, baseURL+"/api/register", `{"username":"Alice","password":"password123"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register failed: %d", resp.StatusCode)
	}
	return findSessionCookie(t, resp.Cookies())
}

func putJSON(t *testing.T, url, body string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put failed: %v", err)
	}
	return resp
}

func decodeAppearanceResponse(t *testing.T, body io.Reader) appearanceResponse {
	t.Helper()
	var response appearanceResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		t.Fatalf("decode appearance response failed: %v", err)
	}
	return response
}
