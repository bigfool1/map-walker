package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"map-walker/internal/realtime"
	"map-walker/internal/synthetic"
)

const testAdminToken = "test-admin-token-abc123"

func newAdminTestServer(t *testing.T, token string) *Server {
	t.Helper()
	srv := newTestServer(t)
	if token != "" {
		srv.WithAdmin(token, stubHubSnapshot, stubSyntheticSnapshot)
	}
	return srv
}

func stubHubSnapshot() *realtime.HubSnapshot {
	return &realtime.HubSnapshot{
		ConnectedClients:    3,
		SimulationTicks:     100,
		ReplicationMessages: 50,
		SampledAt:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func stubSyntheticSnapshot() *synthetic.SyntheticSnapshot {
	return &synthetic.SyntheticSnapshot{
		Target:        5,
		Active:        5,
		TotalMessages: 200,
		SampledAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// --- /admin route ---

func TestAdminRouteReturns404WithoutToken(t *testing.T) {
	srv := newAdminTestServer(t, "")
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/admin")
	if err != nil {
		t.Fatalf("GET /admin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}
}

func TestAdminRouteReturns404ForApiWithoutToken(t *testing.T) {
	srv := newAdminTestServer(t, "")
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/api/admin/synthetic-stats")
	if err != nil {
		t.Fatalf("GET /api/admin/synthetic-stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}
}

// --- /api/admin/synthetic-stats auth ---

func TestAdminStatsReturns401WithMissingAuthorization(t *testing.T) {
	srv := newAdminTestServer(t, testAdminToken)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/api/admin/synthetic-stats")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", resp.StatusCode)
	}
}

func TestAdminStatsReturns401WithWrongToken(t *testing.T) {
	srv := newAdminTestServer(t, testAdminToken)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/admin/synthetic-stats", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", resp.StatusCode)
	}
}

func TestAdminStatsReturns401WithMalformedHeader(t *testing.T) {
	srv := newAdminTestServer(t, testAdminToken)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	// Token without "Bearer " prefix.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/admin/synthetic-stats", nil)
	req.Header.Set("Authorization", testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d want 401 for token without Bearer prefix", resp.StatusCode)
	}
}

func TestAdminStatsReturns405ForNonGet(t *testing.T) {
	srv := newAdminTestServer(t, testAdminToken)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/admin/synthetic-stats", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d want 405", resp.StatusCode)
	}
}

// --- /api/admin/synthetic-stats success ---

func TestAdminStatsReturns200WithCorrectToken(t *testing.T) {
	srv := newAdminTestServer(t, testAdminToken)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/admin/synthetic-stats", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type=%q want application/json", ct)
	}
}

func TestAdminStatsResponseContainsAggregateFields(t *testing.T) {
	srv := newAdminTestServer(t, testAdminToken)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/admin/synthetic-stats", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var body adminStatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if body.Hub == nil {
		t.Fatal("hub snapshot is nil")
	}
	if body.Hub.ConnectedClients != 3 {
		t.Errorf("hub.ConnectedClients=%d want 3", body.Hub.ConnectedClients)
	}
	if body.Hub.ReplicationMessages != 50 {
		t.Errorf("hub.ReplicationMessages=%d want 50", body.Hub.ReplicationMessages)
	}

	if body.Synthetic == nil {
		t.Fatal("synthetic snapshot is nil")
	}
	if body.Synthetic.Target != 5 {
		t.Errorf("synthetic.Target=%d want 5", body.Synthetic.Target)
	}
	if body.Synthetic.TotalMessages != 200 {
		t.Errorf("synthetic.TotalMessages=%d want 200", body.Synthetic.TotalMessages)
	}

	if body.ServedAt.IsZero() {
		t.Error("served_at is zero")
	}
}

func TestAdminStatsResponseContainsNoClientIdentities(t *testing.T) {
	srv := newAdminTestServer(t, testAdminToken)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/admin/synthetic-stats", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Verify no fields for client IDs, usernames, positions, or control actions.
	forbidden := []string{"clients", "users", "positions", "lat", "lng", "start", "stop", "resize"}
	for _, key := range forbidden {
		if _, ok := raw[key]; ok {
			t.Errorf("response contains forbidden field %q", key)
		}
	}
}

func TestAdminStatsNilSnapshotsWhenNoManager(t *testing.T) {
	srv := newTestServer(t)
	// Configured with token but no synthetic provider.
	srv.WithAdmin(testAdminToken, stubHubSnapshot, nil)
	server := httptest.NewServer(srv.Routes())
	t.Cleanup(server.Close)

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/admin/synthetic-stats", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d want 200", resp.StatusCode)
	}

	var body adminStatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Hub == nil {
		t.Error("hub snapshot should be non-nil")
	}
	if body.Synthetic != nil {
		t.Errorf("synthetic snapshot should be nil when no manager, got %+v", body.Synthetic)
	}
}
