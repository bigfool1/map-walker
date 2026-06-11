package server

import (
	"context"
	"net/http"

	"map-walker/internal/realtime"

	"github.com/coder/websocket"
)

type Server struct {
	hub    *realtime.Hub
	static http.Handler
}

func New(hub *realtime.Hub) *Server {
	return &Server{
		hub:    hub,
		static: http.FileServer(http.Dir("web")),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.Handle("/", s.static)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	playerID := r.URL.Query().Get("playerId")
	if playerID == "" {
		http.Error(w, "playerId is required", http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	client := realtime.NewClient(playerID, conn, s.hub)
	client.Run(ctx)

	_ = conn.Close(websocket.StatusNormalClosure, "connection closed")
}
