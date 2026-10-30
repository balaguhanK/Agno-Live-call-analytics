package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"lca-backend/internal/events"
	"lca-backend/internal/store"
)

// Call recordings: the browser phone records both sides of the call and
// uploads the file when the call ends; the dashboard plays it back and the
// post-call analytics page charts its loudness.

const (
	// A 4-hour call at the browser's default Opus bitrate fits comfortably
	maxRecordingBytes = 64 << 20
	// base64 adds a third, plus the JSON around it
	maxRecordingBody = maxRecordingBytes*4/3 + 1024
)

// Recording formats the browser's MediaRecorder produces, by MIME type
var recordingTypes = map[string]string{
	"audio/webm": ".webm",
	"audio/ogg":  ".ogg",
	"audio/mp4":  ".m4a",
}

// Call IDs become file names, so only plain IDs get recordings
var safeCallID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// recordingFile returns the path of the call's recording and its MIME type,
// or "" when there is none.
func (s *Server) recordingFile(callID string) (string, string) {
	if s.recordings == "" || !safeCallID.MatchString(callID) {
		return "", ""
	}
	for mime, ext := range recordingTypes {
		p := filepath.Join(s.recordings, callID+ext)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, mime
		}
	}
	return "", ""
}

// handleUploadRecording serves POST /api/stream/calls/{id}/recording:
// {audio: base64, mimeType}. Only the agent who took the call can upload,
// and only once.
func (s *Server) handleUploadRecording(w http.ResponseWriter, r *http.Request) {
	if s.recordings == "" {
		writeError(w, http.StatusServiceUnavailable, "Call recording is turned off on the server.")
		return
	}
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json.")
		return
	}
	var req struct {
		Audio    string `json:"audio"`
		MimeType string `json:"mimeType"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRecordingBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	ext, ok := recordingTypes[req.MimeType]
	if !ok {
		writeError(w, http.StatusBadRequest, "mimeType must be audio/webm, audio/ogg or audio/mp4.")
		return
	}
	audio, err := base64.StdEncoding.DecodeString(req.Audio)
	if err != nil || len(audio) == 0 || len(audio) > maxRecordingBytes {
		writeError(w, http.StatusBadRequest, "audio must be base64 and at most 64 MB.")
		return
	}
	call, ok := s.ownCall(w, r)
	if !ok {
		return
	}
	if !safeCallID.MatchString(call.ID) {
		writeError(w, http.StatusBadRequest, "This call can't have a recording.")
		return
	}
	if p, _ := s.recordingFile(call.ID); p != "" {
		writeError(w, http.StatusConflict, "This call already has a recording.")
		return
	}
	if err := os.MkdirAll(s.recordings, 0o750); err != nil {
		serverError(w, r, err)
		return
	}
	// Write to a temporary name first so a half-written file is never served
	path := filepath.Join(s.recordings, call.ID+ext)
	tmp := path + ".part"
	if err := os.WriteFile(tmp, audio, 0o640); err != nil {
		serverError(w, r, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		serverError(w, r, err)
		return
	}
	slog.Info("call recording saved", "call", call.ID, "bytes", len(audio))
	call.HasRecording = true
	call.Transcript = nil
	s.publish(r, events.Event{Type: "call", CallID: call.ID, Agent: call.Agent, Call: &call})
	writeJSON(w, http.StatusCreated, call)
}

// handleGetRecording serves GET /api/calls/{id}/recording, with range
// requests so the player can seek.
func (s *Server) handleGetRecording(w http.ResponseWriter, r *http.Request) {
	call, ok := s.visibleCall(w, r)
	if !ok {
		return
	}
	path, mime := s.recordingFile(call.ID)
	if path == "" {
		writeError(w, http.StatusNotFound, "This call has no recording.")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		serverError(w, r, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", st.ModTime().Truncate(time.Second), f)
}

// visibleCall loads the call in the path if the user may see it; other
// agents' calls are reported as missing.
func (s *Server) visibleCall(w http.ResponseWriter, r *http.Request) (store.Call, bool) {
	call, err := s.store.GetCall(r.Context(), r.PathValue("id"))
	filter := agentFilter(current(r).user)
	if errors.Is(err, store.ErrNotFound) || (err == nil && filter != "" && call.Agent != filter) {
		writeError(w, http.StatusNotFound, "Call not found.")
		return store.Call{}, false
	}
	if err != nil {
		serverError(w, r, err)
		return store.Call{}, false
	}
	call.HasRecording = s.hasRecording(call.ID)
	return call, true
}

func (s *Server) hasRecording(callID string) bool {
	p, _ := s.recordingFile(callID)
	return p != ""
}
