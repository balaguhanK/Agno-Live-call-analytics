package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"lca-backend/internal/store"
)

func postJSON(t *testing.T, c *http.Client, url string, body any, out any) int {
	t.Helper()
	data, _ := json.Marshal(body)
	res, err := c.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil && res.StatusCode < 300 {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode
}

func TestStreamCallLifecycle(t *testing.T) {
	srv := newTestServer(t)
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	base := srv.URL + "/api/stream/calls"

	var call store.Call
	if st := postJSON(t, agent, base, map[string]string{"callerPhoneNumber": "+15550009"}, &call); st != http.StatusCreated {
		t.Fatalf("start: %d", st)
	}
	if call.Agent != "BobS" || call.Status != "In Progress" {
		t.Fatalf("call = %+v", call)
	}

	var res streamSegmentResponse
	st := postJSON(t, agent, base+"/"+call.ID+"/segments", map[string]any{
		"speaker": "customer", "text": "This is unacceptable, charge my card 4111 1111 1111 1111 back now!",
		"startSec": 2, "endSec": 6.5,
	}, &res)
	if st != http.StatusCreated {
		t.Fatalf("segment: %d", st)
	}
	line := res.Segments[0]
	if strings.Contains(line.Text, "4111") || !strings.Contains(line.Text, "[CARD]") || line.PII[0] != "CARD" {
		t.Errorf("card not redacted: %q %v", line.Text, line.PII)
	}
	// Each line is scored as it arrives, for the face shown beside it
	if line.Sentiment == nil || *line.Sentiment != "negative" || line.SentimentScore == nil || line.TimeStart != "00:02.0" || line.TimeEnd != "00:06.5" {
		t.Errorf("segment = %+v", line)
	}
	if len(res.Segments) > 1 && res.Segments[1].Sentiment != nil {
		t.Errorf("live-assist tip was scored: %+v", res.Segments[1])
	}
	if len(res.Segments) < 2 || res.Segments[1].Speaker != "AGENT_ASSISTANT" {
		t.Errorf("expected a keyword live-assist tip, got %+v", res.Segments)
	}
	if res.Call.AlertCount != 0 || res.Call.Duration != "00:00:06" {
		t.Errorf("call after segment: alerts=%d duration=%s", res.Call.AlertCount, res.Call.Duration)
	}

	postJSON(t, agent, base+"/"+call.ID+"/segments",
		map[string]any{"speaker": "AGENT", "text": "I'm sorry, I've refunded the charge.", "startSec": 7, "endSec": 9}, nil)

	var ended store.Call
	if st := postJSON(t, agent, base+"/"+call.ID+"/end", map[string]any{"durationSec": 12}, &ended); st != http.StatusOK {
		t.Fatalf("end: %d", st)
	}
	if ended.Status != "Done" || ended.Duration != "00:00:12" || ended.Summary == nil || len(ended.Categories) == 0 {
		t.Fatalf("ended = %+v", ended)
	}
	if !strings.Contains(ended.Summary.Text, "[CARD]") || strings.Contains(ended.Summary.Text, "4111") {
		t.Errorf("summary leaked card data: %s", ended.Summary.Text)
	}
	// Post-call analysis finds the frustrated caller; alerts are alert
	// categories only (a manager escalation), as in LCA
	if ended.AlertCount != 0 || ended.CallerSentiment != "Negative" || len(ended.SentimentHistory) != 2 {
		t.Errorf("post-call analytics: alerts=%d caller=%s history=%d", ended.AlertCount, ended.CallerSentiment, len(ended.SentimentHistory))
	}
	full, _ := postJSONGetCall(t, agent, srv.URL+"/api/calls/"+call.ID)
	for _, seg := range full.Transcript {
		spoken := seg.Speaker == "AGENT" || seg.Speaker == "CUSTOMER"
		if spoken != (seg.Sentiment != nil) {
			t.Errorf("after the call, line %d (%s) sentiment = %v", seg.ID, seg.Speaker, seg.Sentiment)
		}
	}
	if s := full.Transcript[0].Sentiment; s == nil || *s != "negative" {
		t.Errorf("customer line sentiment = %v, want negative", s)
	}

	// No more lines once the call has ended
	st = postJSON(t, agent, base+"/"+call.ID+"/segments",
		map[string]any{"speaker": "AGENT", "text": "hello", "startSec": 13, "endSec": 14}, nil)
	if st != http.StatusConflict {
		t.Errorf("segment after end: %d, want 409", st)
	}
}

func TestStreamRejectsOtherAgentsAndBadInput(t *testing.T) {
	srv := newTestServer(t)
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	babu := signIn(t, srv, "babu@cxplatform.com", "agent123", "agent")
	admin := signIn(t, srv, "admin@cxplatform.com", "admin123", "admin")
	base := srv.URL + "/api/stream/calls"

	var call store.Call
	postJSON(t, bob, base, map[string]string{}, &call)
	seg := map[string]any{"speaker": "AGENT", "text": "hi", "startSec": 0, "endSec": 1}

	if st := postJSON(t, babu, base+"/"+call.ID+"/segments", seg, nil); st != http.StatusNotFound {
		t.Errorf("other agent's call: %d, want 404", st)
	}
	if st := postJSON(t, babu, base+"/"+call.ID+"/end", map[string]any{}, nil); st != http.StatusNotFound {
		t.Errorf("ending other agent's call: %d, want 404", st)
	}
	// The demo admin has no agent ID, so can't stream
	if st := postJSON(t, admin, base, map[string]string{}, nil); st != http.StatusForbidden {
		t.Errorf("admin start: %d, want 403", st)
	}

	for name, body := range map[string]map[string]any{
		"bad speaker": {"speaker": "AGENT_ASSISTANT", "text": "hi", "startSec": 0, "endSec": 1},
		"empty text":  {"speaker": "AGENT", "text": "  ", "startSec": 0, "endSec": 1},
		"long text":   {"speaker": "AGENT", "text": strings.Repeat("a", maxSegmentChars+1), "startSec": 0, "endSec": 1},
		"time order":  {"speaker": "AGENT", "text": "hi", "startSec": 5, "endSec": 1},
	} {
		if st := postJSON(t, bob, base+"/"+call.ID+"/segments", body, nil); st != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, st)
		}
	}
}

// Categories are matched while the call goes on, as in LCA: each new one
// adds a CATEGORY_MATCH line to the transcript, and alert categories count
// as alerts straight away
func TestCategoriesMatchedLive(t *testing.T) {
	srv := newTestServer(t)
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	base := srv.URL + "/api/stream/calls"
	var call store.Call
	postJSON(t, agent, base, map[string]string{}, &call)
	say := func(speaker, text string, at float64) streamSegmentResponse {
		var res streamSegmentResponse
		postJSON(t, agent, base+"/"+call.ID+"/segments", map[string]any{"speaker": speaker, "text": text, "startSec": at, "endSec": at + 2}, &res)
		return res
	}
	matched := func(res streamSegmentResponse) []string {
		var out []string
		for _, s := range res.Segments {
			if s.Speaker == "CATEGORY_MATCH" {
				out = append(out, s.Text)
			}
		}
		return out
	}

	res := say("AGENT", "Thank you for calling, how can I help you today?", 0)
	if got := matched(res); !slices.Equal(got, []string{"AgentGreeting"}) || !slices.Equal(res.Call.Categories, []string{"AgentGreeting"}) {
		t.Errorf("after the greeting: lines %q, categories %q", got, res.Call.Categories)
	}
	res = say("CUSTOMER", "I was charged twice on my bill.", 3)
	if got := matched(res); !slices.Equal(got, []string{"Billing"}) || res.Call.AlertCount != 0 {
		t.Errorf("after the billing line: lines %q, alerts %d", got, res.Call.AlertCount)
	}
	// A category already matched isn't announced again
	if got := matched(say("CUSTOMER", "The bill is wrong.", 6)); len(got) != 0 {
		t.Errorf("repeated category lines %q", got)
	}
	res = say("CUSTOMER", "Let me speak to your manager.", 9)
	if got := matched(res); !slices.Equal(got, []string{"ManagerEscalation"}) || res.Call.AlertCount != 1 {
		t.Errorf("after the escalation: lines %q, alerts %d", got, res.Call.AlertCount)
	}
	if want := []string{"AgentGreeting", "Billing", "ManagerEscalation"}; !slices.Equal(res.Call.Categories, want) {
		t.Errorf("categories = %q, want %q", res.Call.Categories, want)
	}

	var ended store.Call
	postJSON(t, agent, base+"/"+call.ID+"/end", map[string]any{"durationSec": 12}, &ended)
	if ended.AlertCount != 1 || !slices.Equal(ended.Categories, res.Call.Categories) {
		t.Errorf("ended: alerts %d, categories %q", ended.AlertCount, ended.Categories)
	}
}

func TestSettingsAlertPattern(t *testing.T) {
	for name, tc := range map[string]struct {
		alert *regexp.Regexp
		want  string
	}{
		"default":    {nil, "ManagerEscalation"},
		"configured": {regexp.MustCompile("Cancellation|ManagerEscalation"), "Cancellation|ManagerEscalation"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newTestServer(t, func(o *Options) { o.CategoryAlert = tc.alert })
			agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
			resp, err := agent.Get(srv.URL + "/api/settings")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var got map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || got["categoryAlertRegex"] != tc.want {
				t.Errorf("settings = %v (%v), want pattern %q", got, err, tc.want)
			}
		})
	}
	// Signed out: no settings
	srv := newTestServer(t)
	if resp, err := http.Get(srv.URL + "/api/settings"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("signed-out settings = %v %v", resp.StatusCode, err)
	}
}

// A call ended under older rules (the old "HowCanIHelp" filler, an alert
// from frustrated lines) gets the categories and alerts a new call would,
// when the server starts
func TestRecategorizeOldCalls(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, func(o *Options) {
		st := o.Store
		if _, err := st.CreateCall(ctx, store.NewCall{ID: "old1", Agent: "BobS", CallerPhoneNumber: "+15550010",
			InitiationTimestamp: time.Now().UTC().Format(time.RFC3339)}); err != nil {
			t.Fatal(err)
		}
		for _, l := range [][2]string{
			{"CUSTOMER", "I'm really not satisfied with your reservation fee."},
			{"AGENT", "Can I know what is your problem?"},
			{"CUSTOMER", "I have lost my card, and I can't block it. I want to block it."},
		} {
			if _, _, err := st.AppendSegment(ctx, "old1", store.Segment{Speaker: l[0], Text: l[1]}); err != nil {
				t.Fatal(err)
			}
		}
		// Scored by an older word list: the agent's neutral question as negative
		full, _ := st.GetCall(ctx, "old1")
		for _, seg := range full.Transcript {
			if seg.Speaker == "AGENT" {
				_ = st.SetSegmentSentiment(ctx, "old1", seg.ID, "negative", -1)
			}
		}
		done, stale, alerts := "Done", []string{"AgentGreeting"}, 1
		// Saved with three issues, all of which stay
		old := store.CallAnalysis{Issues: []string{"Reservation fee", "Lost card", "Card blocked"}, Model: "gemma2:2b"}
		if _, err := st.UpdateCall(ctx, "old1", store.CallUpdate{Status: &done, Categories: &stale, AlertCount: &alerts, Analysis: &old}); err != nil {
			t.Fatal(err)
		}
		o.Recategorize = true
	})
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	want := []string{"Billing", "CardServices"}
	var got store.Call
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if got, _ = postJSONGetCall(t, agent, srv.URL+"/api/calls/old1"); slices.Equal(got.Categories, want) {
			break
		}
	}
	if !slices.Equal(got.Categories, want) || got.AlertCount != 0 {
		t.Errorf("old call after start-up: categories %q (want %q), alerts %d (want 0)", got.Categories, want, got.AlertCount)
	}
	// Every issue the call has is kept: the caller raised more than one
	if got.Analysis == nil || !slices.Equal(got.Analysis.Issues, []string{"Reservation fee", "Lost card", "Card blocked"}) {
		t.Errorf("old call's issues = %+v, want all of them kept", got.Analysis)
	}
	for _, seg := range got.Transcript {
		if seg.Text == "Can I know what is your problem?" && (seg.Sentiment == nil || *seg.Sentiment != "neutral") {
			t.Errorf("old line not rescored: %+v", seg)
		}
	}
	if got.CallerSentiment != "Negative" {
		t.Errorf("caller sentiment = %q, want Negative (not satisfied, can't block the lost card)", got.CallerSentiment)
	}
}

// A banking word the speech model mishears is fixed before the line is saved
func TestMisheardBankingWordsAreFixed(t *testing.T) {
	srv := newTestServer(t, func(*Options) {})
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	var call store.Call
	postJSON(t, agent, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	var res streamSegmentResponse
	postJSON(t, agent, srv.URL+"/api/stream/calls/"+call.ID+"/segments",
		map[string]any{"speaker": "CUSTOMER", "text": "Actually, I lost my 18 cards.", "startSec": 1, "endSec": 3}, &res)
	if len(res.Segments) == 0 || res.Segments[0].Text != "Actually, I lost my ATM cards." {
		t.Errorf("saved line = %+v", res.Segments)
	}
}
