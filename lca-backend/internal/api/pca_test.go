package api

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"lca-backend/internal/store"
)

// Tests for the live-call and post-call analytics features shown in the
// LCA demo: issue tags, inline assistant answers, recordings, translation,
// questions about a call and the model-written summary and insights.

func TestLinesAreTaggedWithInsights(t *testing.T) {
	srv := newTestServer(t)
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	say := func(speaker, text string) store.Segment {
		var res streamSegmentResponse
		postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments",
			map[string]any{"speaker": speaker, "text": text, "startSec": 1, "endSec": 2}, &res)
		return res.Segments[0]
	}
	if seg := say("CUSTOMER", "Hi, I'm calling about my rewards card"); seg.Insight != store.InsightIssue {
		t.Errorf("issue line insight = %q", seg.Insight)
	}
	if seg := say("AGENT", "I see here that the card has a 2% cashback rate"); seg.Insight != store.InsightOutcome {
		t.Errorf("outcome line insight = %q", seg.Insight)
	}
	// The tag is stored with the line
	got, _ := postJSONGetCall(t, bob, srv.URL+"/api/calls/"+call.ID)
	if len(got.Transcript) == 0 || got.Transcript[0].Insight != store.InsightIssue {
		t.Errorf("stored transcript = %+v", got.Transcript)
	}
}

func TestSuggestionIsAddedToTranscript(t *testing.T) {
	ix := testKB(t)
	w := &fakeWriter{jsonReply: refundJSON}
	srv := newTestServer(t, func(o *Options) { o.KB, o.Writer, o.WriterModel = ix, w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments",
		map[string]any{"speaker": "CUSTOMER", "text": "I was charged twice on card 4111 1111 1111 1111, what is the refund policy", "startSec": 3, "endSec": 6}, nil)

	var assist *store.Segment
	deadline := time.Now().Add(5 * time.Second)
	for assist == nil && time.Now().Before(deadline) {
		got, _ := postJSONGetCall(t, bob, srv.URL+"/api/calls/"+call.ID)
		for i, s := range got.Transcript {
			if s.Assist != nil && !s.Assist.Pending {
				assist = &got.Transcript[i]
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if assist == nil {
		t.Fatal("no inline suggestion in the transcript")
	}
	a := assist.Assist
	wantText := `Say: "Refunds take 3 to 5 business days." Refunds take 3 to 5 business days.`
	if assist.Speaker != "AGENT_ASSISTANT" || assist.Text != wantText || assist.TimeStart != "00:03.0" || a.Model != "" || len(a.Steps) != 1 {
		t.Errorf("inline suggestion = %+v", assist)
	}
	if strings.Contains(a.Query, "4111") || !strings.Contains(a.Query, "[CARD]") {
		t.Errorf("query not redacted: %q", a.Query)
	}
	if len(a.Sources) == 0 || a.Sources[0].Title != "Billing" || a.Sources[0].Excerpt == "" {
		t.Errorf("sources = %+v", a.Sources)
	}
}

func TestCallRecording(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, func(o *Options) { o.Recordings = dir })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	babu := signIn(t, srv, "babu@cxplatform.com", "agent123", "agent")
	admin := signIn(t, srv, "admin@cxplatform.com", "admin123", "admin")
	ended := endedCall(t, srv.URL, bob)
	audio := []byte("OggS fake opus bytes")
	body := map[string]any{"audio": base64.StdEncoding.EncodeToString(audio), "mimeType": "audio/webm"}
	url := srv.URL + "/api/stream/calls/" + ended.ID + "/recording"

	if st := postJSON(t, babu, url, body, nil); st != http.StatusNotFound {
		t.Errorf("other agent's upload = %d, want 404", st)
	}
	if st := postJSON(t, bob, url, map[string]any{"audio": "AAAA", "mimeType": "text/html"}, nil); st != http.StatusBadRequest {
		t.Errorf("bad type upload = %d, want 400", st)
	}
	var saved store.Call
	if st := postJSON(t, bob, url, body, &saved); st != http.StatusCreated || !saved.HasRecording {
		t.Fatalf("upload = %d %+v", st, saved)
	}
	if st := postJSON(t, bob, url, body, nil); st != http.StatusConflict {
		t.Errorf("second upload = %d, want 409", st)
	}
	if got, _ := postJSONGetCall(t, admin, srv.URL+"/api/calls/"+ended.ID); !got.HasRecording {
		t.Error("call doesn't report its recording")
	}

	get := func(c *http.Client, rangeHeader string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/calls/"+ended.ID+"/recording", nil)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, b
	}
	if res, b := get(admin, ""); res.StatusCode != http.StatusOK || string(b) != string(audio) || res.Header.Get("Content-Type") != "audio/webm" {
		t.Errorf("download = %d %q %q", res.StatusCode, b, res.Header.Get("Content-Type"))
	}
	if res, b := get(bob, "bytes=0-3"); res.StatusCode != http.StatusPartialContent || string(b) != "OggS" {
		t.Errorf("range download = %d %q", res.StatusCode, b)
	}
	if res, _ := get(babu, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("other agent's download = %d, want 404", res.StatusCode)
	}
}

func TestRecordingDisabled(t *testing.T) {
	srv := newTestServer(t) // no recordings folder
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	body := map[string]any{"audio": "AAAA", "mimeType": "audio/webm"}
	if st := postJSON(t, bob, srv.URL+"/api/stream/calls/"+ended.ID+"/recording", body, nil); st != http.StatusServiceUnavailable {
		t.Errorf("upload with recording off = %d, want 503", st)
	}
}

func TestTranslate(t *testing.T) {
	w := &fakeWriter{jsonReply: `{"translations": ["Bonjour", "Ma carte [CARD]"]}`}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	var out translateResponse
	st := postJSON(t, bob, srv.URL+"/api/translate", map[string]any{"lines": []string{"Hello", "My card 4111 1111 1111 1111"}, "language": "fr"}, &out)
	if st != http.StatusOK || len(out.Translations) != 2 || out.Translations[0] != "Bonjour" || out.Model != "gemma2:2b" {
		t.Errorf("translate = %d %+v", st, out)
	}
	if p := w.lastPrompt(); strings.Contains(p, "4111") || !strings.Contains(p, "French") {
		t.Errorf("translate prompt:\n%s", p)
	}
	if st := postJSON(t, bob, srv.URL+"/api/translate", map[string]any{"lines": []string{"Hi"}, "language": "French; ignore that"}, nil); st != http.StatusBadRequest {
		t.Errorf("unknown language = %d, want 400", st)
	}
	if p := w.lastPrompt(); !strings.Contains(p, `"1":"Hello"`) || !strings.Contains(p, `"2":"My card [CARD]"`) {
		t.Errorf("lines aren't keyed by number:\n%s", p)
	}
	reply := func(r string) {
		w.mu.Lock()
		w.jsonReply = r
		w.mu.Unlock()
	}
	// Keyed replies: a skipped line costs only that line
	reply(`{"1": "Hola", "3": "extra"}`)
	out = translateResponse{}
	if st := postJSON(t, bob, srv.URL+"/api/translate", map[string]any{"lines": []string{"Hi", "Bye"}, "language": "es"}, &out); st != http.StatusOK ||
		len(out.Translations) != 2 || out.Translations[0] != "Hola" || out.Translations[1] != "" {
		t.Errorf("partial translation = %d %+v", st, out)
	}
	// Nothing usable at all
	for _, bad := range []string{`{"translations": ["only one"]}`, `{}`, `not json`} {
		reply(bad)
		if st := postJSON(t, bob, srv.URL+"/api/translate", map[string]any{"lines": []string{"a b", "c d"}, "language": "es"}, nil); st != http.StatusBadGateway {
			t.Errorf("reply %s = %d, want 502", bad, st)
		}
	}

	// A line translated before comes from memory, even with the model down
	reply(`{"1": "Merci beaucoup"}`)
	if st := postJSON(t, bob, srv.URL+"/api/translate", map[string]any{"lines": []string{"Thanks a lot"}, "language": "fr"}, nil); st != http.StatusOK {
		t.Fatalf("first translation = %d", st)
	}
	w.mu.Lock()
	w.err = errors.New("ollama not running")
	w.mu.Unlock()
	out = translateResponse{}
	if st := postJSON(t, bob, srv.URL+"/api/translate", map[string]any{"lines": []string{"Thanks a lot"}, "language": "fr"}, &out); st != http.StatusOK || out.Translations[0] != "Merci beaucoup" {
		t.Errorf("remembered translation = %d %+v", st, out)
	}
	// ...but only in the language it was translated into
	if st := postJSON(t, bob, srv.URL+"/api/translate", map[string]any{"lines": []string{"Thanks a lot"}, "language": "es"}, nil); st != http.StatusServiceUnavailable {
		t.Errorf("other language with the model down = %d, want 503", st)
	}

	noModel := newTestServer(t)
	agent := signIn(t, noModel, "agent@cxplatform.com", "agent123", "agent")
	if st := postJSON(t, agent, noModel.URL+"/api/translate", map[string]any{"lines": []string{"Hi"}, "language": "fr"}, nil); st != http.StatusServiceUnavailable {
		t.Errorf("translate without model = %d, want 503", st)
	}
}

func TestQueryAboutCall(t *testing.T) {
	w := &fakeWriter{}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	babu := signIn(t, srv, "babu@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	waitForAnalysis(t, srv.URL, ended.ID, bob)

	w.mu.Lock()
	w.reply, w.err = "Yes, the caller thanked the agent.", nil
	w.mu.Unlock()
	res, st := ask(t, bob, srv.URL, map[string]any{"action": "query", "callId": ended.ID, "question": "Was the caller satisfied? Card 4111 1111 1111 1111"})
	if st != http.StatusOK || res.Answer != "Yes, the caller thanked the agent." || res.Model != "gemma2:2b" {
		t.Errorf("query = %d %+v", st, res)
	}
	if p := w.lastPrompt(); strings.Contains(p, "4111") || !strings.Contains(p, "Question: Was the caller satisfied?") || !strings.Contains(p, "CUSTOMER: Great, thank you so much!") {
		t.Errorf("query prompt:\n%s", p)
	}
	if _, st := ask(t, babu, srv.URL, map[string]any{"action": "query", "callId": ended.ID, "question": "Hi?"}); st != http.StatusNotFound {
		t.Errorf("other agent's query = %d, want 404", st)
	}
	if _, st := ask(t, bob, srv.URL, map[string]any{"action": "query", "callId": ended.ID}); st != http.StatusBadRequest {
		t.Errorf("empty query = %d, want 400", st)
	}
	w.mu.Lock()
	w.err = errors.New("ollama not running")
	w.mu.Unlock()
	if res, _ := ask(t, bob, srv.URL, map[string]any{"action": "query", "callId": ended.ID, "question": "Hi?"}); res.Answer != noModelQueryAnswer {
		t.Errorf("query with model down = %+v", res)
	}
}

const testGenAIJSON = `{"summary": "A caller disputed a double charge and the agent refunded it.", "topic": "Duplicate charge",
 "product": "Credit card", "politeness": "Yes", "actions": "The agent refunded the duplicate charge."}`

// Only "Duplicate charge" is kept: the second issue repeats it, the third is
// a sentence and the fourth quotes words the caller never said. Of the
// actions, only the first is backed by what the agent said
const testSummaryJSON = `{"summary": "A caller was charged twice on their card. The agent apologised and refunded the duplicate charge.",
 "topic": "Duplicate charge refund",
 "actions": [{"title": "- Refund the duplicate charge.", "detail": "The agent refunded it on the call.", "owner": "Agent", "due": "none",
   "evidence": "I have refunded the duplicate charge"},
  {"title": "", "detail": "dropped: no title", "evidence": "I have refunded the duplicate charge"},
  {"title": "Ask for the order number", "detail": "made up", "evidence": "Will you please tell me the order number so I can look into it"}],
 "issues": [{"name": "duplicate charge.", "evidence": "I was charged twice on card"},
  {"name": "Duplicate Charge", "evidence": "I was charged twice"},
  {"name": "I was charged twice on my card", "evidence": "I was charged twice"},
  {"name": "Late delivery", "evidence": "my parcel never arrived"}]}`

const testSummaryMD = "**SUMMARY**\n\nA caller was charged twice on their card. The agent apologised and refunded the duplicate charge.\n\n" +
	"**DETAILS**\n\nThe topic of the call is duplicate charge refund.\n\n" +
	"**ACTIONS**\n\n1. **Refund the duplicate charge**\n\nThe agent refunded it on the call.\n\n**Owner:** Agent\n\n**Due Date:** TBD"

// genAIWriter answers the post-call analysis, insights and summary prompts
// with the right shape each.
type genAIWriter struct {
	fakeWriter
	summaryJSON string
}

func (g *genAIWriter) ChatJSON(ctx context.Context, system, user string) (string, error) {
	if strings.Contains(system, "post-call analytics") {
		return testGenAIJSON, nil
	}
	if strings.Contains(system, "record of a finished") {
		return g.summaryJSON, nil
	}
	return g.fakeWriter.ChatJSON(ctx, system, user)
}

func TestModelWritesSummaryAndInsights(t *testing.T) {
	w := &genAIWriter{fakeWriter: fakeWriter{jsonReply: testAnalysisJSON}, summaryJSON: testSummaryJSON}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)

	// Rule-based insights and entities are there straight away
	if g := ended.Analysis.GenAI; g == nil || g.Model != "" || g.Politeness == "" {
		t.Errorf("rule-based insights = %+v", g)
	}
	a := waitForAnalysis(t, srv.URL, ended.ID, bob)
	if g := a.GenAI; g == nil || g.Model != "gemma2:2b" || g.Topic != "Duplicate charge" || g.Politeness != "yes" || g.Product != "Credit card" {
		t.Errorf("model insights = %+v", a.GenAI)
	}
	got, _ := postJSONGetCall(t, bob, srv.URL+"/api/calls/"+ended.ID)
	if got.Summary == nil || got.Summary.Text != testSummaryMD {
		t.Errorf("summary = %+v", got.Summary)
	}
	if !slices.Equal(a.Issues, []string{"Duplicate charge"}) {
		t.Errorf("issues = %q", a.Issues)
	}
	// The summary's actions are kept with the analysis for the PCA page, with
	// the agent's real line as evidence; the made-up one is gone
	if len(a.Actions) != 1 || a.Actions[0].Title != "Refund the duplicate charge" || a.Actions[0].Owner != "Agent" || a.Actions[0].Due != "TBD" ||
		a.Actions[0].Evidence == nil || a.Actions[0].Evidence.Text != "I'm sorry, I have refunded the duplicate charge." {
		t.Errorf("actions = %+v", a.Actions)
	}
}

// Entities are worked out when the call is read, so a call analysed before
// order numbers were recognised still shows them
func TestCallShowsCurrentEntities(t *testing.T) {
	var st Store
	srv := newTestServer(t, func(o *Options) { st = o.Store })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	old := *ended.Analysis
	old.Entities = nil
	if _, err := st.UpdateCall(context.Background(), ended.ID, store.CallUpdate{Analysis: &old}); err != nil {
		t.Fatal(err)
	}
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, nil) // unrelated call
	full, _ := st.GetCall(context.Background(), ended.ID)
	seg := full.Transcript[len(full.Transcript)-1]
	seg.Text = "My order number is QX123, thanks."
	_ = st.UpdateSegment(context.Background(), ended.ID, seg)

	got, _ := postJSONGetCall(t, bob, srv.URL+"/api/calls/"+ended.ID)
	if refs := got.Analysis.Entities[store.EntityReference]; !slices.Equal(refs, []string{"QX123"}) {
		t.Errorf("references = %q", refs)
	}
	if len(got.Analysis.Actions) == 0 || got.Analysis.Actions[0].Owner != "Agent" {
		t.Errorf("rule-based actions = %+v", got.Analysis.Actions)
	}
}

// The live call from the screenshot: the model mentions only the card, but
// the caller also asked to open an account, so that is added back
func TestRequestsTheModelMissesAreKept(t *testing.T) {
	w := &genAIWriter{fakeWriter: fakeWriter{jsonReply: testAnalysisJSON}, summaryJSON: `{
 "summary": "A customer called to report losing their card and asked to block it.",
 "topic": "Card blocking",
 "actions": [{"title": "Block the caller's card", "detail": "The customer asked to block it.", "owner": "Agent", "due": "TBD",
   "evidence": "I want to block my card"}],
 "issues": [{"name": "Lost card", "evidence": "I have lost my card"}]}`}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	for i, l := range []string{"I have lost my card.", "I want to block my card. Also open a new account."} {
		postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments", map[string]any{"speaker": "CUSTOMER", "text": l, "startSec": i * 4, "endSec": i*4 + 3}, nil)
	}
	var ended store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/end", map[string]any{"durationSec": 20}, &ended)
	// Every issue the caller raised, in the order raised, named from their words
	if want := []string{"Lost card", "Account opening"}; !slices.Equal(ended.Analysis.Issues, want) {
		t.Errorf("issues before the model = %q, want %q", ended.Analysis.Issues, want)
	}
	a := waitForAnalysis(t, srv.URL, ended.ID, bob)
	// The model named only the lost card; the account request is still an issue
	if want := []string{"Lost card", "Account opening"}; !slices.Equal(a.Issues, want) {
		t.Errorf("issues = %q, want %q", a.Issues, want)
	}
	var titles []string
	for _, act := range a.Actions {
		titles = append(titles, act.Title)
	}
	if !slices.Equal(titles, []string{"Block the caller's card", "Open a new account"}) {
		t.Errorf("actions = %q", titles)
	}
}

// The call from the screenshot: two issues raised, and the agent only
// promised to block the card. The model's reply is what it wrote then
func TestReservationFeeAndLostCardCall(t *testing.T) {
	w := &genAIWriter{fakeWriter: fakeWriter{jsonReply: testAnalysisJSON}, summaryJSON: `{
 "summary": "A customer called to complain about a reservation fee and requested to block their lost card. The agent blocked the card and provided information on how to proceed.",
 "topic": "Card blocking",
 "actions": [{"title": "Provide link", "detail": "Will provide a link for the customer to leave their details", "owner": "Agent", "due": "TBD", "evidence": "I will provide you a link"},
  {"title": "Block card", "detail": "Blocked the customer's card", "owner": "Agent", "due": "TBD", "evidence": "I'll block your call right now"}],
 "issues": [{"name": "Lost card", "evidence": "I have lost my card, and I can't block it"}]}`}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	for i, l := range [][2]string{
		{"CUSTOMER", "Hello?"},
		{"CUSTOMER", "I'm really not satisfied with your reservation fee."},
		{"AGENT", "Can I know what is your problem?"},
		{"CUSTOMER", "I have lost my card, and I can't block it. I want to block it."},
		{"AGENT", "Okay, ma'am. I'm sorry hear that. I'll block your call right now. I will provide you a link. On the thing, we can leave your details."},
		{"CUSTOMER", "Okay. Thank you."},
	} {
		postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments", map[string]any{"speaker": l[0], "text": l[1], "startSec": i * 5, "endSec": i*5 + 4}, nil)
	}
	var ended store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/end", map[string]any{"durationSec": 40}, &ended)
	if want := []string{"Reservation fee", "Lost card"}; !slices.Equal(ended.Analysis.Issues, want) {
		t.Errorf("issues before the model = %q, want %q", ended.Analysis.Issues, want)
	}
	a := waitForAnalysis(t, srv.URL, ended.ID, bob)
	if want := []string{"Reservation fee", "Lost card"}; !slices.Equal(a.Issues, want) {
		t.Errorf("issues = %q, want %q", a.Issues, want)
	}
	got, _ := postJSONGetCall(t, bob, srv.URL+"/api/calls/"+ended.ID)
	sum := got.Summary.Text
	for _, want := range []string{
		"The agent said they would block the card and would provide information on how to proceed.",
		"2. **Block card**\n\nWill block the customer's card",
		// Both issues, not only the one the model's topic is about
		"The topic of the call is reservation fee and lost card.",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary missing %q:\n%s", want, sum)
		}
	}
	if strings.Contains(sum, "The agent blocked") || strings.Contains(sum, "Blocked the customer's card") {
		t.Errorf("summary still says the card was blocked:\n%s", sum)
	}
}

// Bringing calls up to date at start-up keeps every issue a call has
func TestIssuesOfKeepsEveryIssue(t *testing.T) {
	tr := []store.Segment{
		{ID: 1, Speaker: "CUSTOMER", Text: "I'm really not satisfied with your reservation fee."},
		{ID: 2, Speaker: "CUSTOMER", Text: "I have lost my card, and I can't block it."},
	}
	modelWritten := &store.CallAnalysis{Model: "gemma2:2b", Issues: []string{"Reservation fee", "Lost card"}}
	if got := issuesOf(modelWritten, tr, []string{"Billing"}); !slices.Equal(got, []string{"Reservation fee", "Lost card"}) {
		t.Errorf("model-written issues = %q", got)
	}
	if got := issuesOf(&store.CallAnalysis{}, tr, []string{"Billing"}); !slices.Equal(got, []string{"Reservation fee", "Lost card"}) {
		t.Errorf("rule-based issues = %q", got)
	}
}

// A summary or answer naming numbers nobody said is not shown
func TestInventedNumbersAreRejected(t *testing.T) {
	w := &genAIWriter{fakeWriter: fakeWriter{jsonReply: testAnalysisJSON},
		summaryJSON: strings.Replace(testSummaryJSON, "refunded the duplicate charge.\",", "refunded $75 within 2 days.\",", 1)}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	waitForAnalysis(t, srv.URL, ended.ID, bob)
	got, _ := postJSONGetCall(t, bob, srv.URL+"/api/calls/"+ended.ID)
	if got.Summary == nil || strings.Contains(got.Summary.Text, "$75") || !strings.Contains(got.Summary.Text, "The caller said:") ||
		!strings.Contains(got.Summary.Text, "1. **Refund the duplicate charge**") {
		t.Errorf("summary paragraph with invented numbers kept, or backed-up actions lost:\n%v", got.Summary)
	}

	w.mu.Lock()
	w.reply = "The customer was refunded 120 dollars, reference ZX991."
	w.mu.Unlock()
	res, _ := ask(t, bob, srv.URL, map[string]any{"action": "query", "callId": ended.ID, "question": "How much was refunded?"})
	if res.Answer != unsupportedQueryAnswer {
		t.Errorf("answer with invented facts = %q", res.Answer)
	}
	w.mu.Lock()
	w.reply = "The agent refunded the duplicate charge."
	w.mu.Unlock()
	if res, _ := ask(t, bob, srv.URL, map[string]any{"action": "query", "callId": ended.ID, "question": "What happened?"}); res.Answer != "The agent refunded the duplicate charge." {
		t.Errorf("grounded answer = %q", res.Answer)
	}
}

// Issues are short names; with none from the model, the categories stand in
func TestIssueNames(t *testing.T) {
	got := issueNames([]string{" order cancellation. ", "- Refund", "Order Cancellation", "", "\"Billing\"",
		"I want to cancel the order I placed yesterday", "Late delivery"})
	if want := []string{"Order cancellation", "Refund", "Billing"}; !slices.Equal(got, want) {
		t.Errorf("issueNames = %q, want %q", got, want)
	}
	if got := issueNames(nil); len(got) != 0 {
		t.Errorf("no issues = %q", got)
	}
}

// With no usable issue names from the model, its topic names the issue
func TestIssueFallsBackToTopic(t *testing.T) {
	w := &genAIWriter{fakeWriter: fakeWriter{jsonReply: testAnalysisJSON},
		summaryJSON: testSummaryJSON[:strings.Index(testSummaryJSON, `"issues"`)] + `"issues": []}`}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	// The caller's own words name the issue before the model's topic would
	if a := waitForAnalysis(t, srv.URL, ended.ID, bob); !slices.Equal(a.Issues, []string{"Duplicate charge"}) {
		t.Errorf("issues = %q", a.Issues)
	}
}

func TestIssuesFallBackToCategories(t *testing.T) {
	srv := newTestServer(t) // no model
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	// Without a model the caller's own words name the issue ("I was charged twice" → "Duplicate charge")
	if a := ended.Analysis; a == nil || !slices.Equal(a.Issues, []string{"Duplicate charge"}) {
		t.Errorf("issues = %v for categories %v", a.Issues, ended.Categories)
	}
}

func TestBadModelSummaryKeepsRuleSummary(t *testing.T) {
	w := &fakeWriter{jsonReply: testAnalysisJSON, reply: "Sure!"}
	srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	ended := endedCall(t, srv.URL, bob)
	a := waitForAnalysis(t, srv.URL, ended.ID, bob)
	got, _ := postJSONGetCall(t, bob, srv.URL+"/api/calls/"+ended.ID)
	if got.Summary == nil || !strings.Contains(got.Summary.Text, "The caller said:") || !strings.Contains(got.Summary.Text, "**ACTIONS**") {
		t.Errorf("summary = %+v", got.Summary)
	}
	// The analysis JSON isn't insights JSON, so the rule-based insights stay
	if a.GenAI == nil || a.GenAI.Model != "" {
		t.Errorf("insights = %+v", a.GenAI)
	}
}

// The main issue is the one the caller raised first, whatever order the
// model lists them in; a model issue raised after the call's main topic
// doesn't replace the topic
func TestMainIssueIsRaisedFirst(t *testing.T) {
	head := testSummaryJSON[:strings.Index(testSummaryJSON, `"issues"`)]
	for name, tc := range map[string]struct {
		issues string
		want   []string
	}{
		"earliest of the model's": {`[{"name": "Thanks", "evidence": "Great, thank you so much!"}, {"name": "Duplicate charge", "evidence": "I was charged twice on card"}]`, []string{"Duplicate charge"}},
		// A thank-you isn't a problem: the caller's real issue line is named instead
		"thank-you rejected": {`[{"name": "Thanks", "evidence": "Great, thank you so much!"}]`, []string{"Duplicate charge"}},
	} {
		t.Run(name, func(t *testing.T) {
			w := &genAIWriter{fakeWriter: fakeWriter{jsonReply: testAnalysisJSON}, summaryJSON: head + `"issues": ` + tc.issues + `}`}
			srv := newTestServer(t, func(o *Options) { o.Writer, o.WriterModel = w, "gemma2:2b" })
			bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
			ended := endedCall(t, srv.URL, bob)
			if a := waitForAnalysis(t, srv.URL, ended.ID, bob); !slices.Equal(a.Issues, tc.want) {
				t.Errorf("issues = %q, want %q (categories %q)", a.Issues, tc.want, ended.Categories)
			}
		})
	}
}
