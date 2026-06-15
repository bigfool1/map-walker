package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"map-walker/internal/realtime"
	"map-walker/internal/synthetic"
)

type adminStatsResponse struct {
	Synthetic *synthetic.SyntheticSnapshot `json:"synthetic"`
	Hub       *realtime.HubSnapshot        `json:"hub"`
	ServedAt  time.Time                    `json:"served_at"`
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	if s.adminToken == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, "web/admin.html")
}

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if s.adminToken == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.validAdminToken(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	resp := adminStatsResponse{ServedAt: time.Now()}
	if s.hubSnapshot != nil {
		resp.Hub = s.hubSnapshot()
	}
	if s.syntheticSnapshot != nil {
		resp.Synthetic = s.syntheticSnapshot()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) validAdminToken(r *http.Request) bool {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	token := auth[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) == 1
}
