package server

import (
	"context"
	"errors"
	"net/http"

	"map-walker/internal/auth"
	"map-walker/internal/realtime"

	"github.com/coder/websocket"
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	user, err := s.auth.AuthenticateSession(sessionTokenFromRequest(r))
	if errors.Is(err, auth.ErrUnauthenticated) || errors.Is(err, auth.ErrSessionExpired) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
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

	client := realtime.NewClient(user.ID, user.Username, conn, s.hub)
	client.Run(ctx)

	_ = conn.Close(websocket.StatusNormalClosure, "connection closed")
}
