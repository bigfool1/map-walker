package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"map-walker/internal/auth"
	"map-walker/internal/game"
	"map-walker/internal/storage"
)

type appearanceRequest struct {
	Color string `json:"color"`
	Shape string `json:"shape"`
}

type appearanceResponse struct {
	Color string `json:"color"`
	Shape string `json:"shape"`
}

func (s *Server) handleAppearance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := s.auth.AuthenticateSession(sessionTokenFromRequest(r))
	if errors.Is(err, auth.ErrUnauthenticated) || errors.Is(err, auth.ErrSessionExpired) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthenticated"})
		return
	}
	if err != nil {
		log.Printf("appearance auth failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	var req appearanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request"})
		return
	}

	appearance, err := auth.ValidateAppearance(req.Color, req.Shape)
	if errors.Is(err, auth.ErrInvalidAppearance) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid appearance"})
		return
	}
	if err != nil {
		log.Printf("validate appearance failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := s.auth.SaveAppearance(user.ID, appearance); err != nil {
		log.Printf("save appearance failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if !s.hub.UpdateAppearance(user.ID, gameAppearance(appearance)) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "service unavailable"})
		return
	}

	writeJSON(w, http.StatusOK, appearanceResponseFromStorage(appearance))
}

func appearanceResponseFromStorage(appearance storage.Appearance) appearanceResponse {
	return appearanceResponse{
		Color: appearance.Color,
		Shape: appearance.Shape,
	}
}

func appearanceResponseFromUser(appearance storage.Appearance) appearanceResponse {
	return appearanceResponseFromStorage(appearance)
}

func gameAppearance(appearance storage.Appearance) game.Appearance {
	return game.Appearance{
		Color: appearance.Color,
		Shape: appearance.Shape,
	}
}
