package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"lca-backend/internal/store"
)

var emailPattern = regexp.MustCompile(`^\S+@\S+\.\S+$`)

const minPasswordLen = 6

// userWithCalls adds the number of calls the agent handled.
type userWithCalls struct {
	store.User
	CallCount int `json:"callCount"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	counts, err := s.store.CallCountsByAgent(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	out := make([]userWithCalls, len(users))
	for i, u := range users {
		out[i] = userWithCalls{User: u}
		if u.AgentName != nil {
			out[i].CallCount = counts[*u.AgentName]
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Email     string `json:"email"`
		AgentName string `json:"agentName"`
		Password  string `json:"password"`
		Role      string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	u := store.User{Name: strings.TrimSpace(req.Name), Email: normalizeEmail(req.Email), Role: req.Role}
	if u.Role == "" {
		u.Role = "agent"
	}
	agentName := strings.TrimSpace(req.AgentName)

	switch {
	case u.Name == "":
		writeError(w, http.StatusBadRequest, "Name is required.")
	case !emailPattern.MatchString(u.Email):
		writeError(w, http.StatusBadRequest, "Enter a valid email.")
	case u.Role != "agent" && u.Role != "admin":
		writeError(w, http.StatusBadRequest, "Role must be agent or admin.")
	case u.Role == "agent" && agentName == "":
		writeError(w, http.StatusBadRequest, "Agent ID is required.")
	case len(req.Password) < minPasswordLen:
		writeError(w, http.StatusBadRequest, "Password must be at least 6 characters.")
	default:
		if u.Role == "agent" {
			u.AgentName = &agentName
		}
		hash, err := HashPassword(req.Password)
		if err != nil {
			serverError(w, r, err)
			return
		}
		u.PasswordHash = hash
		created, err := s.store.CreateUser(r.Context(), u)
		switch {
		case errors.Is(err, store.ErrConflict):
			writeError(w, http.StatusConflict, "A user with this email already exists.")
		case errors.Is(err, store.ErrAgentNameTaken):
			writeError(w, http.StatusConflict, "Another user already has this agent ID.")
		case err != nil:
			serverError(w, r, err)
		default:
			writeJSON(w, http.StatusCreated, created)
		}
	}
}

func (s *Server) handleSetUserActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active *bool `json:"active"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Active == nil {
		writeError(w, http.StatusBadRequest, "active is required.")
		return
	}
	email := normalizeEmail(r.PathValue("email"))
	if email == current(r).user.Email {
		writeError(w, http.StatusBadRequest, "You cannot disable your own account.")
		return
	}
	s.respondNoContent(w, r, s.store.SetUserActive(r.Context(), email, *req.Active))
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "Password must be at least 6 characters.")
		return
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		serverError(w, r, err)
		return
	}
	s.respondNoContent(w, r, s.store.SetPasswordHash(r.Context(), normalizeEmail(r.PathValue("email")), hash))
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	email := normalizeEmail(r.PathValue("email"))
	if email == current(r).user.Email {
		writeError(w, http.StatusBadRequest, "You cannot delete your own account.")
		return
	}
	s.respondNoContent(w, r, s.store.DeleteUser(r.Context(), email))
}

func (s *Server) respondNoContent(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "User not found.")
	case err != nil:
		serverError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
