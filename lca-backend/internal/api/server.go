// Package api serves the dashboard's REST and WebSocket endpoints.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"lca-backend/internal/asr"
	"lca-backend/internal/config"
	"lca-backend/internal/events"
	"lca-backend/internal/kb"
	"lca-backend/internal/phone"
	"lca-backend/internal/session"
	"lca-backend/internal/store"
)

const (
	cookieName  = "lca_session"
	sessionTTL  = 8 * time.Hour
	rememberTTL = 30 * 24 * time.Hour
	maxBodySize = 1 << 20
)

// Store is the persistence the API needs; store.Store (Postgres) and
// store.Memory both implement it.
type Store interface {
	GetUser(ctx context.Context, email string) (store.User, error)
	ListUsers(ctx context.Context) ([]store.User, error)
	CreateUser(ctx context.Context, u store.User) (store.User, error)
	SetUserActive(ctx context.Context, email string, active bool) error
	SetPasswordHash(ctx context.Context, email, hash string) error
	TouchSignIn(ctx context.Context, email string) error
	DeleteUser(ctx context.Context, email string) error
	ListCalls(ctx context.Context, q store.CallQuery) ([]store.Call, int, error)
	CallCountsByAgent(ctx context.Context) (map[string]int, error)
	GetCall(ctx context.Context, id string) (store.Call, error)
	CreateCall(ctx context.Context, n store.NewCall) (store.Call, error)
	UpdateCall(ctx context.Context, id string, u store.CallUpdate) (store.Call, error)
	AppendSegment(ctx context.Context, callID string, seg store.Segment) (store.Segment, string, error)
	UpdateSegment(ctx context.Context, callID string, seg store.Segment) error
	SetSegmentSentiment(ctx context.Context, callID string, seq int, label string, score float64) error
}

type Server struct {
	store        Store
	sessions     *session.Store
	hub          *events.Hub
	asr          *asr.Client // nil when no Deepgram key is set
	phone        phone.Config
	kbState               // Agent Assist knowledge base; kb is nil when disabled
	callLocks    sync.Map // call ID → *sync.Mutex, so a call's lines are saved one at a time
	analysing    sync.Map // call IDs whose post-call analysis the model is writing
	translated   translationCache
	recordings   string // folder for call recordings; "" disables them
	ingestToken  string
	cookieSecure bool
	health       func(context.Context) error
	alert        *regexp.Regexp // call categories that are alerts
}

type Options struct {
	Store        Store
	Sessions     *session.Store
	Hub          *events.Hub
	ASR          *asr.Client // optional Deepgram client for live transcription
	Phone        phone.Config
	KB           *kb.Index // optional Agent Assist knowledge base
	Writer       Writer    // optional chat model that writes KB answers
	WriterModel  string
	IngestToken  string
	CookieSecure bool
	StaticDir    string
	Recordings   string // folder for call recordings; "" disables recording
	// Health reports whether dependencies (Postgres, Redis) are reachable
	Health func(context.Context) error
	// Call categories that are alerts; nil means config.DefaultCategoryAlert
	CategoryAlert *regexp.Regexp
	// Recategorize works out every ended call's categories and alerts again
	// at start-up, so calls ended under older rules match the current ones
	Recategorize bool
}

func New(o Options) http.Handler {
	s := &Server{
		store: o.Store, sessions: o.Sessions, hub: o.Hub, asr: o.ASR, phone: o.Phone, kbState: kbState{kb: o.KB, writer: o.Writer, writerModel: o.WriterModel},
		ingestToken: o.IngestToken, cookieSecure: o.CookieSecure, health: o.Health, recordings: o.Recordings,
		alert: o.CategoryAlert,
	}
	if s.alert == nil {
		s.alert = regexp.MustCompile(config.DefaultCategoryAlert)
	}
	if o.Recategorize {
		go s.recategorize(context.Background())
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.Handle("GET /api/auth/me", s.requireUser(s.handleMe))
	mux.Handle("GET /api/settings", s.requireUser(s.handleSettings))

	mux.Handle("GET /api/users", s.requireAdmin(s.handleListUsers))
	mux.Handle("POST /api/users", s.requireAdmin(s.handleCreateUser))
	mux.Handle("PATCH /api/users/{email}", s.requireAdmin(s.handleSetUserActive))
	mux.Handle("PUT /api/users/{email}/password", s.requireAdmin(s.handleResetPassword))
	mux.Handle("DELETE /api/users/{email}", s.requireAdmin(s.handleDeleteUser))

	mux.Handle("GET /api/calls", s.requireUser(s.handleListCalls))
	mux.Handle("GET /api/calls/export", s.requireUser(s.handleExportCalls))
	mux.Handle("GET /api/calls/{id}", s.requireUser(s.handleGetCall))
	mux.Handle("POST /api/calls/{id}/analysis", s.requireUser(s.handleAnalyseCall))
	mux.Handle("GET /api/calls/{id}/recording", s.requireUser(s.handleGetRecording))
	mux.Handle("POST /api/translate", s.requireUser(s.handleTranslate))
	mux.Handle("GET /ws", s.requireUser(s.handleWebSocket))

	// Live transcription from the dashboard's Stream Audio page
	mux.Handle("GET /api/stream/config", s.requireUser(s.handleStreamConfig))
	mux.Handle("GET /api/phone/config", s.requireUser(s.handlePhoneConfig))

	// Agent Assist Bot
	mux.Handle("POST /api/assist", s.requireUser(s.handleAssist))
	mux.Handle("GET /api/assist/status", s.requireUser(s.handleAssistStatus))
	mux.Handle("POST /api/assist/reload", s.requireAdmin(s.handleAssistReload))
	mux.Handle("GET /ws/stream/{id}", s.requireUser(s.handleStreamAudio))
	mux.Handle("POST /api/stream/test", s.requireUser(s.handleStreamTest))
	mux.Handle("POST /api/stream/calls", s.requireUser(s.handleStreamStart))
	mux.Handle("POST /api/stream/calls/{id}/segments", s.requireUser(s.handleStreamSegment))
	mux.Handle("POST /api/stream/calls/{id}/end", s.requireUser(s.handleStreamEnd))
	mux.Handle("POST /api/stream/calls/{id}/recording", s.requireUser(s.handleUploadRecording))

	// Machine-to-machine endpoints for FreeSWITCH and ASR workers
	mux.Handle("POST /api/ingest/calls", s.requireIngest(s.handleIngestCreateCall))
	mux.Handle("PATCH /api/ingest/calls/{id}", s.requireIngest(s.handleIngestUpdateCall))
	mux.Handle("POST /api/ingest/calls/{id}/segments", s.requireIngest(s.handleIngestSegment))
	mux.Handle("GET /api/ingest/agents", s.requireIngest(s.handleIngestAgents))
	mux.Handle("POST /api/ingest/calls/{id}/lines", s.requireIngest(s.handleIngestLine))
	mux.Handle("POST /api/ingest/calls/{id}/end", s.requireIngest(s.handleIngestEnd))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "Not found.")
	})
	if o.StaticDir != "" {
		mux.Handle("/", spaHandler(o.StaticDir))
	}
	return logRequests(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.health(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── Auth middleware ────────────────────────────────────────

type ctxKey struct{}

type authed struct {
	user  store.User
	sess  session.Session
	token string
}

func current(r *http.Request) authed { return r.Context().Value(ctxKey{}).(authed) }

func (s *Server) requireUser(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cookie-authenticated writes must be JSON. Browsers can't send that
		// cross-site without a CORS preflight, which blocks CSRF.
		if r.Method != http.MethodGet && !isJSON(r) {
			writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json.")
			return
		}
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		sess, err := s.sessions.Get(r.Context(), cookie.Value)
		if errors.Is(err, session.ErrNoSession) {
			s.clearCookie(w)
			writeError(w, http.StatusUnauthorized, "Your session has expired. Sign in again.")
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}
		// Re-read the user so disabling or deleting an account takes effect immediately
		user, err := s.store.GetUser(r.Context(), sess.Email)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !user.Active) {
			_ = s.sessions.Delete(r.Context(), cookie.Value)
			s.clearCookie(w)
			writeError(w, http.StatusUnauthorized, "This account is no longer active.")
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, authed{user: user, sess: sess, token: cookie.Value})
		next(w, r.WithContext(ctx))
	})
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.Handler {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if current(r).user.Role != "admin" {
			writeError(w, http.StatusForbidden, "Only admins can manage users.")
			return
		}
		next(w, r)
	})
}

func (s *Server) requireIngest(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.ingestToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "Invalid ingest token.")
			return
		}
		next(w, r)
	})
}

func (s *Server) setCookie(w http.ResponseWriter, token string, remember bool) {
	c := &http.Cookie{
		Name: cookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode,
	}
	if remember {
		c.MaxAge = int(rememberTTL.Seconds())
	}
	http.SetCookie(w, c)
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

// ── JSON helpers ───────────────────────────────────────────

func isJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json.")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "Something went wrong. Try again.")
}

// ── Logging ────────────────────────────────────────────────

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer (needed for WebSocket hijacking).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start).Round(time.Millisecond))
	})
}

// handleSettings serves GET /api/settings: what the dashboard needs to show
// calls the way the server judges them, e.g. which categories are alerts.
func (s *Server) handleSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"categoryAlertRegex": s.alert.String()})
}
