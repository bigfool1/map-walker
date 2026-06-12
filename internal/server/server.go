package server

import (
	"net/http"

	"map-walker/internal/auth"
	"map-walker/internal/realtime"
)

type Server struct {
	hub    *realtime.Hub
	auth   *auth.Service
	static http.Handler
}

func New(hub *realtime.Hub, authService *auth.Service) *Server {
	return &Server{
		hub:    hub,
		auth:   authService,
		static: http.FileServer(http.Dir("web")),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/register", s.handleRegister)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/session", s.handleSession)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.Handle("/", s.static)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
