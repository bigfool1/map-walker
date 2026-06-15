package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"map-walker/internal/auth"
)

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := s.auth.AuthenticateSession(sessionTokenFromRequest(r))
	if errors.Is(err, auth.ErrUnauthenticated) || errors.Is(err, auth.ErrSessionExpired) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := s.hub.Leaderboard(user.ID)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("encode leaderboard response: %v", err)
	}
}
