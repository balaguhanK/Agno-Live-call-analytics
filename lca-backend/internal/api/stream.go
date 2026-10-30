package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"

	"lca-backend/internal/analysis"
	"lca-backend/internal/asr"
	"lca-backend/internal/events"
	"lca-backend/internal/store"
)

// Streaming endpoints let a signed-in agent turn browser speech recognition
// into a live call. Text is redacted and analyzed here, never in the browser.

const (
	maxSegmentChars  = 2000
	maxStreamSeconds = 4 * 60 * 60
	statusInProgress = "In Progress"
	statusDone       = "Done"
)

var errNotStreamer = errors.New("Only agents with an agent ID can stream calls. Ask an admin to set one on the Users page.")

// streamAgent is the agent ID calls are recorded under for this user.
func streamAgent(u store.User) (string, error) {
	if u.AgentName == nil || *u.AgentName == "" {
		return "", errNotStreamer
	}
	return *u.AgentName, nil
}

// ownCall loads a call the current user is streaming. Calls owned by anyone
// else are reported as missing.
func (s *Server) ownCall(w http.ResponseWriter, r *http.Request) (store.Call, bool) {
	agent, err := streamAgent(current(r).user)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return store.Call{}, false
	}
	call, err := s.store.GetCall(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && call.Agent != agent) {
		writeError(w, http.StatusNotFound, "Call not found.")
		return store.Call{}, false
	}
	if err != nil {
		serverError(w, r, err)
		return store.Call{}, false
	}
	return call, true
}

func (s *Server) handleStreamStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CallerPhoneNumber string `json:"callerPhoneNumber"`
	}
	if !decode(w, r, &req) {
		return
	}
	agent, err := streamAgent(current(r).user)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	phone := strings.TrimSpace(req.CallerPhoneNumber)
	if len(phone) > 32 {
		writeError(w, http.StatusBadRequest, "callerPhoneNumber is too long.")
		return
	}
	call, err := s.store.CreateCall(r.Context(), store.NewCall{
		ID: newUUID(), Agent: agent, CallerPhoneNumber: phone, SystemPhoneNumber: "Browser",
	})
	if err != nil {
		serverError(w, r, err)
		return
	}
	s.publish(r, events.Event{Type: "call", CallID: call.ID, Agent: call.Agent, Call: &call})
	writeJSON(w, http.StatusCreated, call)
}

type streamSegmentResponse struct {
	// The saved line, followed by any live-assist suggestions it triggered
	Segments []store.Segment `json:"segments"`
	Call     store.Call      `json:"call"`
}

func (s *Server) handleStreamSegment(w http.ResponseWriter, r *http.Request) {
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

	call, ok := s.ownCall(w, r)
	if !ok {
		return
	}
	res, err := s.saveLine(r.Context(), call.ID, req.Speaker, req.Text, req.StartSec, req.EndSec)
	if errors.Is(err, errCallEnded) {
		writeError(w, http.StatusConflict, "This call has ended.")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

var errCallEnded = errors.New("call has ended")

// saveLine redacts and stores one spoken line of an in-progress call, adds
// any live-assist tips it triggers and publishes it all. The line gets its
// own sentiment straight away, for the face shown beside it; the call's
// averages, trends and alerts stay post-call analysis, done by endCall.
// Both the HTTP and the Deepgram paths use it.
func (s *Server) saveLine(ctx context.Context, callID, speaker, rawText string, startSec, endSec float64) (streamSegmentResponse, error) {
	// Agent and customer streams of one call save concurrently; tips depend
	// on the lines before them, so take turns
	lock, _ := s.callLocks.LoadOrStore(callID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	call, err := s.store.GetCall(ctx, callID)
	if err != nil {
		return streamSegmentResponse{}, err
	}
	if call.Status != statusInProgress {
		return streamSegmentResponse{}, errCallEnded
	}

	// Redact before anything is stored or broadcast
	// Banking words the speech model mishears ("18 card" for "ATM card") are fixed first
	text, pii := analysis.Redact(asr.Correct(rawText))
	seg := store.Segment{
		Speaker: speaker, Text: text, PII: pii,
		TimeStart: stamp(startSec), TimeEnd: stamp(endSec),
		Insight: analysis.DetectInsight(speaker, text, analysis.CountInsights(call.Transcript, store.InsightIssue)),
	}
	seg = analysis.ScoreTranscript([]store.Segment{seg})[0]
	saved, agent, err := s.store.AppendSegment(ctx, call.ID, seg)
	saved.InsightText = analysis.InsightPhrase(saved)
	if err != nil {
		return streamSegmentResponse{}, err
	}
	out := []store.Segment{saved}
	s.publishCtx(ctx, events.Event{Type: "segment", CallID: call.ID, Agent: agent, Segment: &saved})
	if speaker == analysis.SpeakerCustomer {
		s.maybeSuggest(call.ID, agent, saved)
	}

	// With the live assistant on, it writes the one guidance line per customer line
	var tips []string
	if s.kb == nil && s.writer == nil {
		tips = analysis.Suggestions(saved, call.Transcript)
	}
	for _, tip := range tips {
		assist, _, err := s.store.AppendSegment(ctx, call.ID, store.Segment{
			Speaker: analysis.SpeakerAssistant, Text: tip, TimeStart: saved.TimeStart, TimeEnd: saved.TimeEnd,
		})
		if err != nil {
			return streamSegmentResponse{}, err
		}
		out = append(out, assist)
		s.publishCtx(ctx, events.Event{Type: "segment", CallID: call.ID, Agent: agent, Segment: &assist})
	}

	duration := clock(endSec)
	update := store.CallUpdate{Duration: &duration}
	// Categories are matched as the call goes, as in LCA: each new one gets a
	// line in the transcript, and the call's categories and alerts update
	cats := analysis.Categories(append(slices.Clone(call.Transcript), saved))
	if added := analysis.NewCategories(call.Categories, cats); len(added) > 0 {
		for _, c := range added {
			match, _, err := s.store.AppendSegment(ctx, call.ID, store.Segment{
				Speaker: analysis.SpeakerCategory, Text: c, TimeStart: saved.TimeStart, TimeEnd: saved.TimeEnd,
			})
			if err != nil {
				return streamSegmentResponse{}, err
			}
			out = append(out, match)
			s.publishCtx(ctx, events.Event{Type: "segment", CallID: call.ID, Agent: agent, Segment: &match})
		}
		alerts := analysis.AlertCount(cats, s.alert)
		update.Categories, update.AlertCount = &cats, &alerts
	}
	updated, err := s.store.UpdateCall(ctx, call.ID, update)
	if err != nil {
		return streamSegmentResponse{}, err
	}
	s.publishCtx(ctx, events.Event{Type: "call", CallID: updated.ID, Agent: updated.Agent, Call: &updated})
	return streamSegmentResponse{Segments: out, Call: updated}, nil
}

// handleStreamEnd marks the call Done and writes its categories and summary.
// Ending an already-ended call returns it unchanged.
func (s *Server) handleStreamEnd(w http.ResponseWriter, r *http.Request) {
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
	call, ok := s.ownCall(w, r)
	if !ok {
		return
	}
	ended, err := s.endCall(r.Context(), call, req.DurationSec)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ended)
}

// endCall marks a call Done and runs post-call analysis: sentiment for every
// line, sentiment averages, trends and alerts, categories, the summary and
// the caller / query / agent analysis.
// A call that has already ended is returned unchanged.
func (s *Server) endCall(ctx context.Context, call store.Call, durationSec float64) (store.Call, error) {
	if call.Status == statusDone {
		return call, nil
	}
	transcript := analysis.ScoreTranscript(call.Transcript)
	for i, seg := range transcript {
		if call.Transcript[i].SentimentScore != nil || seg.SentimentScore == nil {
			continue // already scored (e.g. by an ingest worker) or not a spoken line
		}
		if err := s.store.SetSegmentSentiment(ctx, call.ID, seg.ID, *seg.Sentiment, *seg.SentimentScore); err != nil {
			return store.Call{}, err
		}
	}
	call.Transcript = transcript
	categories := analysis.Categories(call.Transcript)
	update := analysis.Aggregate(call.Transcript)
	status, duration, alerts := statusDone, clock(durationSec), analysis.AlertCount(categories, s.alert)
	update.Status, update.Duration, update.Categories, update.AlertCount = &status, &duration, &categories, &alerts
	ended, err := s.store.UpdateCall(ctx, call.ID, update)
	if err != nil {
		return store.Call{}, err
	}
	s.callLocks.Delete(call.ID)
	s.suggestions.Delete(call.ID)
	summary := analysis.Summarize(ended, call.Transcript, categories)
	callAnalysis := s.newAnalysis(call.Transcript, categories)
	ended, err = s.store.UpdateCall(ctx, call.ID, store.CallUpdate{Summary: &summary, Analysis: &callAnalysis})
	if err != nil {
		return store.Call{}, err
	}
	s.publishCtx(ctx, events.Event{Type: "call", CallID: ended.ID, Agent: ended.Agent, Call: &ended})
	// The model's insights follow in another "call" event
	ended.Transcript = call.Transcript
	s.startAnalysis(ended)
	ended.Transcript = nil
	return ended, nil
}

// stamp formats seconds as mm:ss.s, matching transcript timestamps.
func stamp(sec float64) string {
	m := int(sec) / 60
	return fmt.Sprintf("%02d:%04.1f", m, sec-float64(m*60))
}

// clock formats seconds as hh:mm:ss, matching call durations.
func clock(sec float64) string {
	n := int(sec)
	return fmt.Sprintf("%02d:%02d:%02d", n/3600, n/60%60, n%60)
}

// recategorize brings every ended call up to date with the current
// analysis: each spoken line is scored again (its face), the call's
// sentiment, trend, categories and alerts are worked out again, and every
// issue it has is kept. Calls ended under older rules then show what a new
// call would.
func (s *Server) recategorize(ctx context.Context) {
	calls, _, err := s.store.ListCalls(ctx, store.CallQuery{})
	if err != nil {
		slog.Warn("bringing calls up to date failed", "err", err)
		return
	}
	updated, lines := 0, 0
	for _, c := range calls {
		if c.Status != statusDone {
			continue
		}
		full, err := s.store.GetCall(ctx, c.ID)
		if err != nil {
			slog.Warn("bringing a call up to date failed", "call", c.ID, "err", err)
			continue
		}
		// Issue, action and outcome labels follow the current rules too: a line
		// flagged as an issue by older rules may be a request for information
		if full.Transcript, err = s.redetectInsights(ctx, full); err != nil {
			slog.Warn("bringing a call up to date failed", "call", c.ID, "err", err)
			continue
		}
		transcript, err := s.rescore(ctx, c.ID, full.Transcript)
		if err != nil {
			slog.Warn("bringing a call up to date failed", "call", c.ID, "err", err)
			continue
		}
		for i := range transcript {
			if was, now := full.Transcript[i].SentimentScore, transcript[i].SentimentScore; now != nil && (was == nil || *was != *now) {
				lines++
			}
		}
		cats := analysis.Categories(transcript)
		alerts := analysis.AlertCount(cats, s.alert)
		update := analysis.Aggregate(transcript)
		update.Categories, update.AlertCount = &cats, &alerts
		if a := full.Analysis; a != nil && !a.Pending {
			// The stats and quotes follow the new scores; the model's words stay
			fixed := *a
			fresh := analysis.Analyze(transcript)
			fixed.Stats, fixed.Quotes, fixed.Issues = fresh.Stats, fresh.Quotes, issuesOf(a, transcript, cats)
			if a.Model == "" {
				fixed.Caller, fixed.Agent = fresh.Caller, fresh.Agent
			}
			update.Analysis = &fixed
		}
		if _, err := s.store.UpdateCall(ctx, c.ID, update); err != nil {
			slog.Warn("bringing a call up to date failed", "call", c.ID, "err", err)
			continue
		}
		updated++
	}
	slog.Info("calls up to date with the current analysis", "calls", updated, "linesRescored", lines)
}

// issuesOf is every issue an ended call shows: the ones written with the
// model, kept as they are, else each issue the caller raised, named from
// their own words.
func issuesOf(a *store.CallAnalysis, transcript []store.Segment, categories []string) []string {
	if a.Model != "" {
		// What the model wrote stays, except names that are really requests for information
		var kept []string
		for _, name := range a.Issues {
			if !analysis.IsInquiryName(name) {
				kept = append(kept, name)
			}
		}
		if len(kept) > 0 {
			return kept
		}
		if analysis.HasInquiry(transcript) {
			return nil
		}
	}
	var kept []string
	for _, name := range analysis.RuleIssues(transcript, categories) {
		if !analysis.IsInquiryName(name) {
			kept = append(kept, name)
		}
	}
	return kept
}
