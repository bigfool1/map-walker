package server

import (
	"encoding/json"
	"net/http"
	"time"

	"map-walker/internal/realtime"
	"map-walker/internal/synthetic"
)

type statsResponse struct {
	Synthetic *synthetic.SyntheticSnapshot `json:"synthetic"`
	Hub       *realtime.HubSnapshot        `json:"hub"`
	ServedAt  time.Time                    `json:"served_at"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/stats.html")
}

func (s *Server) handleStatsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := statsResponse{ServedAt: time.Now()}
	if s.hubSnapshot != nil {
		resp.Hub = s.hubSnapshot()
	}
	if s.syntheticSnapshot != nil {
		resp.Synthetic = s.syntheticSnapshot()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
