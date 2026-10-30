package api

import (
	"errors"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"lca-backend/internal/session"
	"lca-backend/internal/store"
)

// HashPassword is exported so the seeder uses the same cost as the API.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// Compared against when the email is unknown, so response time doesn't reveal which emails exist
var dummyHash, _ = HashPassword("timing-equalizer")

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

type sessionResponse struct {
	store.User
	ExpiresAt int64 `json:"expiresAt"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Remember bool   `json:"remember"`
	}
	if !decode(w, r, &req) {
		return
	}

	user, err := s.store.GetUser(r.Context(), normalizeEmail(req.Email))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		serverError(w, r, err)
		return
	}
	hash := user.PasswordHash
	if hash == "" {
		hash = dummyHash
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil || user.Email == "" {
		writeError(w, http.StatusUnauthorized, "Incorrect email or password.")
		return
	}
	if user.Role != req.Role {
		other := "Agent"
		if user.Role == "admin" {
			other = "Admin"
		}
		writeError(w, http.StatusForbidden, "This account is not an "+req.Role+" account. Switch to the "+other+" tab.")
		return
	}
	if !user.Active {
		writeError(w, http.StatusForbidden, "This account has been disabled. Contact your administrator.")
		return
	}

	ttl := sessionTTL
	if req.Remember {
		ttl = rememberTTL
	}
	token, sess, err := s.sessions.Create(r.Context(), user.Email, ttl)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if err := s.store.TouchSignIn(r.Context(), user.Email); err != nil {
		serverError(w, r, err)
		return
	}
	s.setCookie(w, token, req.Remember)
	writeJSON(w, http.StatusOK, sessionResponse{User: user, ExpiresAt: sess.ExpiresAt})
}

// handleLogout works without a valid session so a stale cookie can always be cleared.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		if err := s.sessions.Delete(r.Context(), cookie.Value); err != nil && !errors.Is(err, session.ErrNoSession) {
			serverError(w, r, err)
			return
		}
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := current(r)
	writeJSON(w, http.StatusOK, sessionResponse{User: a.user, ExpiresAt: a.sess.ExpiresAt})
}
