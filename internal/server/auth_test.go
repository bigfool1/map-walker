package server

import (
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

func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })
	return New(hub, auth.NewService(db))
}

func TestRegisterCreatesSession(t *testing.T) {
	srv := newTestServer(t)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	resp := postJSON(t, server.URL+"/api/register", `{"username":"Alice","password":"password123"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := decodeSessionResponse(t, resp.Body)
	if body.Username != "Alice" || body.UserID == "" {
		t.Fatalf("unexpected body: %+v", body)
	}

	cookie := findSessionCookie(t, resp.Cookies())
	assertSessionCookie(t, cookie, false)
}

func TestRegisterRejectsDuplicateUsername(t *testing.T) {
	srv := newTestServer(t)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	first := postJSON(t, server.URL+"/api/register", `{"username":"Alice","password":"password123"}`)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first register failed: %d", first.StatusCode)
	}

	resp := postJSON(t, server.URL+"/api/register", `{"username":"alice","password":"another-password"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}

	errBody := decodeErrorResponse(t, resp.Body)
	if errBody.Error != "username unavailable" {
		t.Fatalf("unexpected error: %q", errBody.Error)
	}
}

func TestRegisterRejectsValidationFailures(t *testing.T) {
	srv := newTestServer(t)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	resp := postJSON(t, server.URL+"/api/register", `{"username":"ab","password":"password123"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for username, got %d", resp.StatusCode)
	}

	resp = postJSON(t, server.URL+"/api/register", `{"username":"alice","password":"short"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for password, got %d", resp.StatusCode)
	}
}

func TestLoginAcceptsCaseVariant(t *testing.T) {
	srv := newTestServer(t)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	register := postJSON(t, server.URL+"/api/register", `{"username":"Alice","password":"password123"}`)
	register.Body.Close()

	resp := postJSON(t, server.URL+"/api/login", `{"username":"alice","password":"password123"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := decodeSessionResponse(t, resp.Body)
	if body.Username != "Alice" {
		t.Fatalf("unexpected username: %q", body.Username)
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	srv := newTestServer(t)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	register := postJSON(t, server.URL+"/api/register", `{"username":"Alice","password":"password123"}`)
	register.Body.Close()

	resp := postJSON(t, server.URL+"/api/login", `{"username":"Alice","password":"wrong-password"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	errBody := decodeErrorResponse(t, resp.Body)
	if errBody.Error != "invalid username or password" {
		t.Fatalf("unexpected error: %q", errBody.Error)
	}
}

func TestSessionLookupReturnsAuthenticatedUser(t *testing.T) {
	srv := newTestServer(t)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	register := postJSON(t, server.URL+"/api/register", `{"username":"Alice","password":"password123"}`)
	cookie := findSessionCookie(t, register.Cookies())
	register.Body.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("session request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeSessionResponse(t, resp.Body)
	if body.Username != "Alice" {
		t.Fatalf("unexpected session body: %+v", body)
	}
}

func TestSessionLookupRejectsExpiredSession(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	authService := auth.NewService(db)
	srv := New(realtime.NewHub(), authService)

	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	if err := db.CreateUser(storage.User{
		ID:                 "user-1",
		Username:           "Alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          now.Add(-31 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	token := "expired-token"
	if err := db.CreateSession(storage.Session{
		TokenHash: auth.HashSessionToken(token),
		UserID:    "user-1",
		CreatedAt: now.Add(-31 * 24 * time.Hour),
		ExpiresAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("session request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLogoutClearsSessionCookie(t *testing.T) {
	srv := newTestServer(t)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	register := postJSON(t, server.URL+"/api/register", `{"username":"Alice","password":"password123"}`)
	cookie := findSessionCookie(t, register.Cookies())
	register.Body.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/logout", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("logout request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	clearCookie := findSessionCookie(t, resp.Cookies())
	if clearCookie.Value != "" || clearCookie.MaxAge != -1 {
		t.Fatalf("expected cleared cookie, got %+v", clearCookie)
	}

	sessionReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("new session request failed: %v", err)
	}
	sessionReq.AddCookie(cookie)

	sessionResp, err := http.DefaultClient.Do(sessionReq)
	if err != nil {
		t.Fatalf("session request failed: %v", err)
	}
	defer sessionResp.Body.Close()
	if sessionResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", sessionResp.StatusCode)
	}
}

func TestSessionCookieSecureOnHTTPS(t *testing.T) {
	srv := newTestServer(t)
	server := httptest.NewTLSServer(srv.Routes())
	t.Cleanup(server.Close)

	client := server.Client()
	resp, err := client.Post(
		server.URL+"/api/register",
		"application/json",
		strings.NewReader(`{"username":"Alice","password":"password123"}`),
	)
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	defer resp.Body.Close()

	cookie := findSessionCookie(t, resp.Cookies())
	if !cookie.Secure {
		t.Fatalf("expected secure cookie over HTTPS, got %+v", cookie)
	}
	assertSessionCookie(t, cookie, true)
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	return resp
}

func decodeSessionResponse(t *testing.T, body io.Reader) sessionResponse {
	t.Helper()
	var response sessionResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		t.Fatalf("decode session response failed: %v", err)
	}
	return response
}

func decodeErrorResponse(t *testing.T, body io.Reader) errorResponse {
	t.Helper()
	var response errorResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		t.Fatalf("decode error response failed: %v", err)
	}
	return response
}

func findSessionCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == auth.CookieName {
			return cookie
		}
	}
	t.Fatalf("session cookie not found")
	return nil
}

func assertSessionCookie(t *testing.T, cookie *http.Cookie, secure bool) {
	t.Helper()
	if !cookie.HttpOnly {
		t.Fatalf("expected HttpOnly cookie, got %+v", cookie)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected SameSite=Lax, got %+v", cookie)
	}
	if cookie.MaxAge != int(auth.SessionDuration/time.Second) {
		t.Fatalf("expected 30-day max age, got %d", cookie.MaxAge)
	}
	if cookie.Secure != secure {
		t.Fatalf("expected secure=%v, got %+v", secure, cookie)
	}
}
