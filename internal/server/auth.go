package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"map-walker/internal/auth"
)

type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req credentialsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request"})
		return
	}

	token, user, err := s.auth.Register(req.Username, req.Password)
	if errors.Is(err, auth.ErrInvalidUsername) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "username must be 3 to 32 characters"})
		return
	}
	if errors.Is(err, auth.ErrInvalidPassword) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "password must be at least 8 characters"})
		return
	}
	if errors.Is(err, auth.ErrUsernameUnavailable) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "username unavailable"})
		return
	}
	if err != nil {
		log.Printf("register failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, sessionResponse{UserID: user.ID, Username: user.Username})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req credentialsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request"})
		return
	}

	token, user, err := s.auth.Login(req.Username, req.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid username or password"})
		return
	}
	if err != nil {
		log.Printf("login failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, sessionResponse{UserID: user.ID, Username: user.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := sessionTokenFromRequest(r)
	if err := s.auth.Logout(token); err != nil && !errors.Is(err, auth.ErrUnauthenticated) {
		log.Printf("logout failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := s.auth.AuthenticateSession(sessionTokenFromRequest(r))
	if errors.Is(err, auth.ErrUnauthenticated) || errors.Is(err, auth.ErrSessionExpired) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthenticated"})
		return
	}
	if err != nil {
		log.Printf("session lookup failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, sessionResponse{UserID: user.ID, Username: user.Username})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func sessionTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func cookieSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, auth.NewSessionCookie(token, cookieSecure(r)))
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, auth.ClearSessionCookie(cookieSecure(r)))
}
