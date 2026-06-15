package server

import (
	"net/http"

	"map-walker/internal/auth"
	"map-walker/internal/realtime"
	"map-walker/internal/synthetic"
)

type Server struct {
	hub               *realtime.Hub
	auth              *auth.Service
	static            http.Handler
	hubSnapshot       func() *realtime.HubSnapshot
	syntheticSnapshot func() *synthetic.SyntheticSnapshot
}

func New(hub *realtime.Hub, authService *auth.Service) *Server {
	return &Server{
		hub:    hub,
		auth:   authService,
		static: http.FileServer(http.Dir("web")),
	}
}

// WithAdmin configures snapshot providers for the admin dashboard.
// Call before Routes(). Returns the receiver for chaining.
func (s *Server) WithAdmin(
	hubFn func() *realtime.HubSnapshot,
	synFn func() *synthetic.SyntheticSnapshot,
) *Server {
	s.hubSnapshot = hubFn
	s.syntheticSnapshot = synFn
	return s
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/register", s.handleRegister)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/session", s.handleSession)
	mux.HandleFunc("/api/appearance", s.handleAppearance)
	mux.HandleFunc("/api/leaderboard/online", s.handleLeaderboard)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/api/stats/synthetic", s.handleStatsAPI)
	mux.Handle("/", s.static)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
