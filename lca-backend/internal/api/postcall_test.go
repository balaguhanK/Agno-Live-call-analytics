package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"lca-backend/internal/store"
)

const testAnalysisJSON = `{
 "caller": {"mood": "Frustrated then relieved", "summary": "The caller was **upset** about a double charge, then relieved."},
 "query": {"issue": "Refund a duplicate charge", "outcome": "resolved", "detail": "The agent refunded it.", "evidence": "I have refunded the duplicate charge."},
 "agent": {"tone": "Calm and polite", "summary": "The agent apologised and fixed it quickly.",
  "strengths": ["- Apologised", "Fixed it on the call", "", "Clear", "Fourth point"], "improvements": ["Confirm the amount"], "rating": "4"}}`

// endedCall runs a short call for bob and ends it.
func endedCall(t *testing.T, srv string, bob *http.Client) store.Call {
	t.Helper()
	var call store.Call
	postJSON(t, bob, srv+"/api/stream/calls", map[string]string{}, &call)
	for _, l := range []map[string]any{
		{"speaker": "AGENT", "text": "Thank you for calling, how can I help?", "startSec": 1, "endSec": 2},
		{"speaker": "CUSTOMER", "text": "I was charged twice on card 4111 1111 1111 1111, this is terrible.", "startSec": 3, "endSec": 6},
		{"speaker": "AGENT", "text": "I'm sorry, I have refunded the duplicate charge.", "startSec": 7, "endSec": 9},
		{"speaker": "CUSTOMER", "text": "Great, thank you so much!", "startSec": 10, "endSec": 11},
	} {
		postJSON(t, bob, srv+"/api/stream/calls/"+call.ID+"/segments", l, nil)
	}
	var ended store.Call
	if st := postJSON(t, bob, srv+"/api/stream/calls/"+call.ID+"/end", map[string]any{"durationSec": 12}, &ended); st != http.StatusOK {
		t.Fatalf("end: %d", st)
	}
	return ended
}

// waitForAnalysis polls the call until the model has finished its analysis.
func waitForAnalysis(t *testing.T, srv, id string, c *http.Client) *store.CallAnalysis {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		call, _ := postJSONGetCall(t, c, srv+"/api/calls/"+id)
		if call.Analysis != nil && !call.Analysis.Pending {
			return call.Analysis
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("analysis still pending")
	return nil
}

func TestPostCallAnalysisWithModel(t *testing.T) {
	w := &fakeWriter{jsonReply: testAnalysisJSON}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")

	ended := endedCall(t, srv.URL, bob)
	// Stats and quotes come back at once; the model's insights follow
	a := ended.Analysis
	if a == nil || !a.Pending || a.Stats.CallerLines != 2 || a.Stats.CallerStart != "Negative" || a.Stats.CallerEnd != "Positive" {
		t.Fatalf("analysis at end = %+v", a)
	}
	if q := a.Quotes.CallerLow; q == nil || !strings.Contains(q.Text, "[CARD], this is terrible.") {
		t.Errorf("caller quote = %+v", q)
	}

	a = waitForAnalysis(t, srv.URL, ended.ID, bob)
	if a.Model != "gemma2:2b" || a.Caller.Mood != "Frustrated then relieved" || a.Caller.Summary != "The caller was upset about a double charge, then relieved." {
		t.Errorf("caller = %+v (model %q)", a.Caller, a.Model)
	}
	if a.Query.Issue != "Refund a duplicate charge" || a.Query.Outcome != "Resolved" || a.Query.Evidence == nil ||
		a.Query.Evidence.Text != "I'm sorry, I have refunded the duplicate charge." {
		t.Errorf("query = %+v (evidence %+v)", a.Query, a.Query.Evidence)
	}
	if a.Agent.Rating != 4 || len(a.Agent.Strengths) != 3 || a.Agent.Strengths[0] != "Apologised" || a.Agent.Improvements[0] != "Confirm the amount" {
		t.Errorf("agent = %+v", a.Agent)
	}
	if a.Stats.AgentLines != 2 || a.Quotes.AgentBest == nil {
		t.Errorf("model run lost the stats or quotes: %+v / %+v", a.Stats, a.Quotes)
	}
	if p := w.lastPrompt(); strings.Contains(p, "4111") || !strings.Contains(p, "CUSTOMER: I was charged twice on card [CARD]") {
		t.Errorf("analysis prompt:\n%s", p)
	}
}

func TestPostCallAnalysisFallsBack(t *testing.T) {
	for name, w := range map[string]*fakeWriter{
		"model down":  {err: errors.New("ollama not running")},
		"bad JSON":    {jsonReply: "Sure! Here is the analysis"},
		"empty reply": {jsonReply: `{"caller": {}, "agent": {}}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
			bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
			ended := endedCall(t, srv.URL, bob)
			a := waitForAnalysis(t, srv.URL, ended.ID, bob)
			if a.Model != "" || a.Caller.Mood != "Negative → Positive" || a.Query.Issue == "" || a.Agent.Tone == "" {
				t.Errorf("fallback analysis = %+v", a)
			}
		})
	}
}

func TestPostCallAnalysisWithoutModel(t *testing.T) {
	srv := newTestServer(t)
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	if a := ended.Analysis; a == nil || a.Pending || a.Model != "" || a.Caller.Mood != "Negative → Positive" {
		t.Errorf("rule-based analysis = %+v", a)
	}
}

func TestAnalyseCallEndpoint(t *testing.T) {
	w := &fakeWriter{jsonReply: testAnalysisJSON}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	babu := signIn(t, srv, "babu@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	waitForAnalysis(t, srv.URL, ended.ID, bob)

	if st := postJSON(t, babu, srv.URL+"/api/calls/"+ended.ID+"/analysis", map[string]any{}, nil); st != http.StatusNotFound {
		t.Errorf("other agent's call status = %d, want 404", st)
	}
	var live store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &live)
	if st := postJSON(t, bob, srv.URL+"/api/calls/"+live.ID+"/analysis", map[string]any{}, nil); st != http.StatusConflict {
		t.Errorf("live call status = %d, want 409", st)
	}

	// Re-analysing runs the model again
	w.mu.Lock()
	w.jsonReply = strings.Replace(testAnalysisJSON, `"rating": "4"`, `"rating": 5`, 1)
	w.mu.Unlock()
	var again store.Call
	if st := postJSON(t, bob, srv.URL+"/api/calls/"+ended.ID+"/analysis", map[string]any{}, &again); st != http.StatusAccepted || again.Analysis == nil || !again.Analysis.Pending {
		t.Fatalf("re-analyse = %d %+v", st, again.Analysis)
	}
	if a := waitForAnalysis(t, srv.URL, ended.ID, bob); a.Agent.Rating != 5 {
		t.Errorf("re-analysed rating = %d", a.Agent.Rating)
	}
}

// A call summarised before the current format (and with no issue flagged)
// gets both redone by "Analyse again", even with no model
func TestAnalyseAgainUpgradesOldCall(t *testing.T) {
	var st Store
	srv := newTestServer(t, func(o *Options) { st = o.Store })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)

	ctx := context.Background()
	old := "**SUMMARY**\n\nCall handled by BobS.\n\n_Generated automatically from the transcript._"
	if _, err := st.UpdateCall(ctx, ended.ID, store.CallUpdate{Summary: &old}); err != nil {
		t.Fatal(err)
	}
	full, _ := st.GetCall(ctx, ended.ID)
	for _, seg := range full.Transcript {
		seg.Insight = ""
		_ = st.UpdateSegment(ctx, ended.ID, seg)
	}

	if s := postJSON(t, bob, srv.URL+"/api/calls/"+ended.ID+"/analysis", map[string]any{}, nil); s != http.StatusAccepted {
		t.Fatalf("analyse again = %d", s)
	}
	got, _ := postJSONGetCall(t, bob, srv.URL+"/api/calls/"+ended.ID)
	if got.Summary == nil || strings.Contains(got.Summary.Text, "Generated automatically") ||
		!strings.Contains(got.Summary.Text, "**DETAILS**\n\nThe topic of the call is") || !strings.Contains(got.Summary.Text, "**ACTIONS**") {
		t.Errorf("summary not upgraded:\n%v", got.Summary)
	}
	issues := 0
	for _, seg := range got.Transcript {
		if seg.Insight == store.InsightIssue {
			issues++
			if !strings.HasPrefix(seg.Text, "I was charged twice") {
				t.Errorf("unexpected issue line %q", seg.Text)
			}
		}
	}
	if issues != 1 {
		t.Errorf("issues flagged = %d, want 1", issues)
	}
}

// What the model can't back up with the transcript is never shown
func TestModelClaimsMustMatchTheCall(t *testing.T) {
	made := strings.NewReplacer(
		`"evidence": "I have refunded the duplicate charge."`, `"evidence": "I have sent you a $50 voucher"`,
		`"The caller was **upset** about a double charge, then relieved."`, `"The caller was charged 3 times, 90 dollars in all."`,
	).Replace(testAnalysisJSON)
	w := &fakeWriter{jsonReply: made}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	a := waitForAnalysis(t, srv.URL, ended.ID, bob)
	// The outcome's evidence was never said, so no outcome is claimed
	if a.Query.Outcome != "" || a.Query.Detail != "" || a.Query.Evidence != nil {
		t.Errorf("unbacked outcome kept: %+v", a.Query)
	}
	// "3 times, 90 dollars" was never said: the transcript-based caller insight stays
	if strings.Contains(a.Caller.Summary, "90") || a.Caller.Mood != "Negative → Positive" {
		t.Errorf("caller insight with invented numbers kept: %+v", a.Caller)
	}
}

func TestAnalysisHelpers(t *testing.T) {
	for in, want := range map[string]string{"Resolved": "Resolved", "partially resolved": "Partly resolved", "Not resolved": "Not resolved",
		"unresolved": "Not resolved", "unknown": ""} {
		if got := outcome(in); got != want {
			t.Errorf("outcome(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]int{"4": 4, `"3"`: 3, "4.6": 5, "0": 0, "9": 0, `"great"`: 0, "": 0} {
		if got := rating([]byte(in)); got != want {
			t.Errorf("rating(%s) = %d, want %d", in, got, want)
		}
	}
}

// "Analyse again" scores the lines again with the current word list and
// recomputes the call's sentiment, so calls scored by an older list are fixed
func TestAnalyseAgainRescoresSentiment(t *testing.T) {
	var st Store
	srv := newTestServer(t, func(o *Options) { st = o.Store })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)

	ctx := context.Background()
	full, _ := st.GetCall(ctx, ended.ID)
	for _, seg := range full.Transcript {
		if seg.Speaker == "CUSTOMER" {
			_ = st.SetSegmentSentiment(ctx, ended.ID, seg.ID, "neutral", 0)
		}
	}
	flat := "Flat"
	if _, err := st.UpdateCall(ctx, ended.ID, store.CallUpdate{CallerSentimentTrend: &flat}); err != nil {
		t.Fatal(err)
	}

	if s := postJSON(t, bob, srv.URL+"/api/calls/"+ended.ID+"/analysis", map[string]any{}, nil); s != http.StatusAccepted {
		t.Fatalf("analyse again = %d", s)
	}
	got, _ := postJSONGetCall(t, bob, srv.URL+"/api/calls/"+ended.ID)
	for _, seg := range got.Transcript {
		if strings.Contains(seg.Text, "this is terrible") && (seg.Sentiment == nil || *seg.Sentiment != "negative") {
			t.Errorf("line not rescored: %+v", seg)
		}
	}
	// The caller went from "this is terrible" to "thank you so much"
	if got.CallerSentimentTrend != "Up" {
		t.Errorf("call sentiment not recomputed: trend %s", got.CallerSentimentTrend)
	}
}
