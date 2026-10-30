package api

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"lca-backend/internal/analysis"
	"lca-backend/internal/events"
	"lca-backend/internal/store"
)

// agentFilter is the agent whose calls the user may see; "" means all calls.
func agentFilter(u store.User) string {
	if u.Role == "admin" {
		return ""
	}
	if u.AgentName == nil {
		return "\x00" // agent without an ID sees nothing
	}
	return *u.AgentName
}

const (
	defaultPageSize = 25
	maxPageSize     = 100
	maxWindowHours  = 24 * 90
	maxSearchLen    = 100
	maxExportRows   = 10000
	maxExportIDs    = 500
)

// callQuery reads the filters shared by the list and export endpoints:
// hours (0 or absent = all time), q, sort, order (asc|desc, default desc).
func callQuery(r *http.Request) (store.CallQuery, error) {
	v := r.URL.Query()
	q := store.CallQuery{
		Agent:  agentFilter(current(r).user),
		Search: strings.TrimSpace(v.Get("q")),
		Sort:   v.Get("sort"),
		Desc:   v.Get("order") != "asc",
	}
	if len(q.Search) > maxSearchLen {
		return q, fmt.Errorf("q must be at most %d characters", maxSearchLen)
	}
	if q.Sort == "" {
		q.Sort = store.DefaultSort
	} else if _, ok := store.SortKeys[q.Sort]; !ok {
		return q, fmt.Errorf("unknown sort %q", q.Sort)
	}
	if o := v.Get("order"); o != "" && o != "asc" && o != "desc" {
		return q, errors.New("order must be asc or desc")
	}
	hours, err := intParam(v.Get("hours"), 0, 0, maxWindowHours)
	if err != nil {
		return q, fmt.Errorf("hours: %w", err)
	}
	if hours > 0 {
		q.Since = time.Now().Add(-time.Duration(hours) * time.Hour)
	}
	return q, nil
}

// intParam parses an optional integer query value within [lo, hi].
func intParam(raw string, fallback, lo, hi int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("must be a whole number from %d to %d", lo, hi)
	}
	return n, nil
}

type callPage struct {
	Items    []store.Call `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"pageSize"`
}

// handleListCalls serves GET /api/calls?hours=&q=&sort=&order=&page=&pageSize=
func (s *Server) handleListCalls(w http.ResponseWriter, r *http.Request) {
	q, err := callQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v := r.URL.Query()
	page, err := intParam(v.Get("page"), 1, 1, math.MaxInt32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "page: "+err.Error())
		return
	}
	size, err := intParam(v.Get("pageSize"), defaultPageSize, 1, maxPageSize)
	if err != nil {
		writeError(w, http.StatusBadRequest, "pageSize: "+err.Error())
		return
	}
	q.Limit, q.Offset = size, (page-1)*size

	calls, total, err := s.store.ListCalls(r.Context(), q)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, callPage{Items: calls, Total: total, Page: page, PageSize: size})
}

// handleExportCalls serves GET /api/calls/export as CSV, with the list's
// filters, or only the comma-separated ids when given.
func (s *Server) handleExportCalls(w http.ResponseWriter, r *http.Request) {
	q, err := callQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if ids := r.URL.Query().Get("ids"); ids != "" {
		q.IDs = strings.Split(ids, ",")
		if len(q.IDs) > maxExportIDs {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("ids: at most %d calls", maxExportIDs))
			return
		}
	}
	q.Limit = maxExportRows
	calls, _, err := s.store.ListCalls(r.Context(), q)
	if err != nil {
		serverError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="calls-`+time.Now().UTC().Format("20060102-150405")+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Call ID", "Agent", "Initiation timestamp", "Caller phone number", "System phone number",
		"Status", "Caller sentiment", "Caller average sentiment", "Caller sentiment trend", "Duration",
		"Categories", "Alerts", "Summary"})
	for _, c := range calls {
		summary := ""
		if c.Summary != nil {
			summary = c.Summary.Text
		}
		row := []string{c.ID, c.Agent, c.InitiationTimestamp, c.CallerPhoneNumber, c.SystemPhoneNumber,
			c.Status, c.CallerSentiment, strconv.FormatFloat(c.CallerAvgSentiment, 'f', 3, 64), c.CallerSentimentTrend,
			c.Duration, strings.Join(c.Categories, "; "), strconv.Itoa(c.AlertCount), summary}
		for i := range row {
			row[i] = csvSafe(row[i])
		}
		_ = cw.Write(row)
	}
	cw.Flush()
}

// csvSafe stops spreadsheet apps from running a cell as a formula. Phone
// numbers like +1703... and negative numbers are left alone.
func csvSafe(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '@', 0x09, 0x0d: // tab, carriage return
		return "'" + v
	case '+', '-':
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return "'" + v
		}
	}
	return v
}

func (s *Server) handleGetCall(w http.ResponseWriter, r *http.Request) {
	// Other agents' calls are reported as missing rather than forbidden
	call, ok := s.visibleCall(w, r)
	if !ok {
		return
	}
	// A finished call that matched no category still shows one, from its issues or topic
	if call.Status == statusDone && !analysis.HasTopicCategory(call.Categories) {
		var issues []string
		topic := ""
		if call.Analysis != nil {
			issues = call.Analysis.Issues
			if call.Analysis.GenAI != nil {
				topic = call.Analysis.GenAI.Topic
			}
		}
		call.Categories = append(slices.Clone(call.Categories), analysis.FallbackCategory(issues, topic))
	}
	// The words of each issue, action or outcome line, for underlining
	for i := range call.Transcript {
		call.Transcript[i].InsightText = analysis.InsightPhrase(call.Transcript[i])
	}
	// Worked out fresh, so calls analysed before a kind of entity was
	// recognised (e.g. order numbers) still show it
	if call.Analysis != nil {
		call.Analysis.Entities = analysis.Entities(call.Transcript)
		// The agent by their full name on the dashboard, next to the customer
		if people := analysis.People(call.Transcript, s.agentFullName(r.Context(), call.Agent)); len(people) > 0 {
			call.Analysis.Entities[store.EntityPerson] = people
		}
		call.Analysis.Highlights = analysis.Highlights(call.Transcript)
		call.Analysis.CategoryLines = analysis.CategoryLines(call.Transcript, call.Categories)
		// Calls analysed before actions were stored keep them in the summary
		if call.Analysis.Actions == nil && call.Summary != nil {
			call.Analysis.Actions = analysis.ParseSummaryActions(call.Summary.Text)
		}
	}
	writeJSON(w, http.StatusOK, call)
}

// agentFullName is the dashboard name ("Bob Smith") of the agent with this
// agent name ("BobS"), or "" when there's none.
func (s *Server) agentFullName(ctx context.Context, agent string) string {
	if agent == "" {
		return ""
	}
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		slog.Warn("listing users for the agent's name failed", "err", err)
		return ""
	}
	for _, u := range users {
		if u.AgentName != nil && *u.AgentName == agent {
			return u.Name
		}
	}
	return ""
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	a := current(r)
	// Default origin check: the Origin host must match the request Host
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the error response
	}
	defer conn.CloseNow()

	// Close the socket when the session expires
	ctx, cancel := context.WithDeadline(r.Context(), time.UnixMilli(a.sess.ExpiresAt))
	defer cancel()
	ctx = conn.CloseRead(ctx)

	client := s.hub.Register(agentFilter(a.user))
	defer s.hub.Unregister(client)

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "session ended")
			return
		case msg, ok := <-client.Send:
			if !ok {
				conn.Close(websocket.StatusTryAgainLater, "too slow")
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
				return
			}
		case <-ping.C:
			if err := conn.Ping(ctx); err != nil {
				return
			}
		}
	}
}

// ── Ingest (FreeSWITCH / ASR workers) ──────────────────────

func (s *Server) handleIngestCreateCall(w http.ResponseWriter, r *http.Request) {
	var req store.NewCall
	if !decode(w, r, &req) {
		return
	}
	if req.ID == "" {
		req.ID = newUUID()
	}
	if req.InitiationTimestamp != "" {
		if _, err := time.Parse(time.RFC3339Nano, req.InitiationTimestamp); err != nil {
			writeError(w, http.StatusBadRequest, "initiationTimestamp must be RFC 3339.")
			return
		}
	}
	call, err := s.store.CreateCall(r.Context(), req)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "A call with this ID already exists.")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	s.publish(r, events.Event{Type: "call", CallID: call.ID, Agent: call.Agent, Call: &call})
	writeJSON(w, http.StatusCreated, call)
}

func (s *Server) handleIngestUpdateCall(w http.ResponseWriter, r *http.Request) {
	var req store.CallUpdate
	if !decode(w, r, &req) {
		return
	}
	call, err := s.store.UpdateCall(r.Context(), r.PathValue("id"), req)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Call not found.")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	s.publish(r, events.Event{Type: "call", CallID: call.ID, Agent: call.Agent, Call: &call})
	writeJSON(w, http.StatusOK, call)
}

func (s *Server) handleIngestSegment(w http.ResponseWriter, r *http.Request) {
	var req store.Segment
	if !decode(w, r, &req) {
		return
	}
	req.Speaker = strings.ToUpper(strings.TrimSpace(req.Speaker))
	if req.Speaker == "" || strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "speaker and text are required.")
		return
	}
	callID := r.PathValue("id")
	seg, agent, err := s.store.AppendSegment(r.Context(), callID, req)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Call not found.")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	s.publish(r, events.Event{Type: "segment", CallID: callID, Agent: agent, Segment: &seg})
	writeJSON(w, http.StatusCreated, seg)
}

// publish logs rather than fails: the data is already saved, and clients
// pick it up on their next fetch even if the live event is lost.
func (s *Server) publish(r *http.Request, e events.Event) { s.publishCtx(r.Context(), e) }

func (s *Server) publishCtx(ctx context.Context, e events.Event) {
	if err := s.hub.Publish(ctx, e); err != nil {
		slog.Error("publish event", "type", e.Type, "call", e.CallID, "err", err)
	}
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
