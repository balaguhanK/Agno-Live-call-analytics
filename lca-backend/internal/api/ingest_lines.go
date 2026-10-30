package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"lca-backend/internal/analysis"
	"lca-backend/internal/store"
)

// Ingest endpoints for recorders (the SIPREC SRS) that transcribe calls
// themselves. Authenticated with INGEST_TOKEN, not a user session.

// handleIngestAgents serves GET /api/ingest/agents: active agent IDs, so a
// recorder can tell which call participant is the agent.
func (s *Server) handleIngestAgents(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	agents := []string{}
	for _, u := range users {
		if u.Active && u.AgentName != nil && *u.AgentName != "" {
			agents = append(agents, *u.AgentName)
		}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"agents": agents})
}

// handleIngestLine serves POST /api/ingest/calls/{id}/lines: one transcribed
// line, run through the same redaction and analysis as live streaming.
func (s *Server) handleIngestLine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Speaker  string  `json:"speaker"`
		Text     string  `json:"text"`
		StartSec float64 `json:"startSec"`
		EndSec   float64 `json:"endSec"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Speaker = strings.ToUpper(strings.TrimSpace(req.Speaker))
	req.Text = strings.TrimSpace(req.Text)
	switch {
	case req.Speaker != analysis.SpeakerAgent && req.Speaker != analysis.SpeakerCustomer:
		writeError(w, http.StatusBadRequest, "speaker must be AGENT or CUSTOMER.")
		return
	case req.Text == "" || len(req.Text) > maxSegmentChars:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("text must be 1 to %d characters.", maxSegmentChars))
		return
	case req.StartSec < 0 || req.EndSec < req.StartSec || req.EndSec > maxStreamSeconds:
		writeError(w, http.StatusBadRequest, "startSec and endSec must be in order and within the call.")
		return
	}
	res, err := s.saveLine(r.Context(), r.PathValue("id"), req.Speaker, req.Text, req.StartSec, req.EndSec)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Call not found.")
	case errors.Is(err, errCallEnded):
		writeError(w, http.StatusConflict, "This call has ended.")
	case err != nil:
		serverError(w, r, err)
	default:
		writeJSON(w, http.StatusCreated, res)
	}
}

// handleIngestEnd serves POST /api/ingest/calls/{id}/end.
func (s *Server) handleIngestEnd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DurationSec float64 `json:"durationSec"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.DurationSec < 0 || req.DurationSec > maxStreamSeconds {
		writeError(w, http.StatusBadRequest, "durationSec is out of range.")
		return
	}
	call, err := s.store.GetCall(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Call not found.")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	ended, err := s.endCall(r.Context(), call, req.DurationSec)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ended)
}
