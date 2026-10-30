package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"lca-backend/internal/events"
	"lca-backend/internal/store"
)

// liveEvents opens the agent's live event socket, like the dashboard does,
// and delivers its "assist" events on a channel. (Reads aren't given a
// timeout: this WebSocket library closes the socket when a read times out.)
func liveEvents(t *testing.T, srv *httptest.Server, c *http.Client) <-chan events.Event {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", &websocket.DialOptions{HTTPClient: c})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	out := make(chan events.Event, 16)
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var ev events.Event
			if json.Unmarshal(data, &ev) == nil && ev.Type == "assist" {
				out <- ev
			}
		}
	}()
	return out
}

// finalSuggestion returns the next suggestion the model has finished
// (skipping the instant policy card), or nil if none arrives in time.
func finalSuggestion(t *testing.T, live <-chan events.Event, wait time.Duration) *events.Event {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case ev := <-live:
			if !ev.Assist.Pending {
				return &ev
			}
		case <-deadline:
			return nil
		}
	}
}

// nextSuggestion returns the next suggestion, or nil if none arrives in time.
func nextSuggestion(t *testing.T, live <-chan events.Event, wait time.Duration) *events.Event {
	t.Helper()
	select {
	case ev := <-live:
		return &ev
	case <-time.After(wait):
		return nil
	}
}

// The model may still send steps; they are ignored for the policy's own
const refundJSON = `{"say": "I'm sorry about that, I'm **refunding** the duplicate charge now [1].", "steps": ["Made-up step"]}`

func TestProactiveSuggestions(t *testing.T) {
	ix := testKB(t)
	w := &fakeWriter{jsonReply: refundJSON}
	srv := newTestServer(t, func(o *Options) { o.KB, o.Writer, o.WriterModel = ix, w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	babu := signIn(t, srv, "babu@cxplatform.com", "agent123", "agent")
	bobLive, babuLive := liveEvents(t, srv, bob), liveEvents(t, srv, babu)

	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	say := func(speaker, text string) {
		postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments",
			map[string]any{"speaker": speaker, "text": text, "startSec": 1, "endSec": 2}, nil)
	}

	say("CUSTOMER", "I was charged twice on card 4111 1111 1111 1111, what is the refund policy")
	// The policy's own reply arrives at once and is final: no waiting for the model
	ev := nextSuggestion(t, bobLive, 5*time.Second)
	if ev == nil {
		t.Fatal("no suggestion pushed to the agent")
	}
	sg := ev.Assist
	if ev.CallID != call.ID || sg.Pending || sg.SegmentID == 0 || sg.Model != "" || sg.Say != "Refunds take 3 to 5 business days." ||
		len(sg.Steps) != 1 || len(sg.Sources) == 0 || sg.Sources[0].Title != "Billing" {
		t.Errorf("suggestion = %+v / %+v", ev, sg)
	}
	if strings.Contains(sg.Trigger, "4111") || !strings.Contains(sg.Trigger, "[CARD]") {
		t.Errorf("trigger not redacted: %q", sg.Trigger)
	}
	if nextSuggestion(t, babuLive, 500*time.Millisecond) != nil {
		t.Error("another agent received this call's suggestion")
	}

	// Every customer line is guided: the same topic again, and a plain "okay thanks"
	say("CUSTOMER", "So about that refund for the double charge")
	if ev := nextSuggestion(t, bobLive, 5*time.Second); ev == nil || ev.Assist.Say == "" {
		t.Errorf("repeated topic got no guidance: %+v", ev)
	}
	say("CUSTOMER", "okay thanks")
	if ev := nextSuggestion(t, bobLive, 5*time.Second); ev == nil || ev.Assist.Say != acknowledgementReply {
		t.Errorf("acknowledgement guidance = %+v", ev)
	}
	// The agent's own lines are not guided
	say("AGENT", "I have refunded the duplicate charge for you")
	if ev := nextSuggestion(t, bobLive, time.Second); ev != nil {
		t.Errorf("guidance for an agent line: %+v", ev.Assist)
	}

	// A different topic in the same call
	say("CUSTOMER", "Also I forgot my password, what are the steps to sign in")
	if ev := nextSuggestion(t, bobLive, 5*time.Second); ev == nil || ev.Assist.Sources[0].Title != "Access" ||
		len(ev.Assist.Steps) != 1 || ev.Assist.Steps[0] != "Reset a password from the sign-in page." {
		t.Errorf("second-topic suggestion = %+v", ev)
	}
	// Lines a policy covers never wait for the model
	if w.prompt != "" {
		t.Errorf("the model was used for a policy match: %q", w.prompt)
	}
}

func TestIsAcknowledgement(t *testing.T) {
	for text, want := range map[string]bool{
		"Okay thank you, that is very helpful.": true, "okay thanks": true, "Thank you": true,
		"That's all, thanks": true, "Okay can you check my balance?": false,
		"ok so I have a problem with my card and it is blocked since yesterday morning": false,
		"I lost my card": false,
	} {
		if got := isAcknowledgement(text); got != want {
			t.Errorf("isAcknowledgement(%q) = %v", text, got)
		}
	}
}

func TestSuggestionFallsBackToPolicyText(t *testing.T) {
	ix := testKB(t)
	srv := newTestServer(t, func(o *Options) { o.KB = ix }) // no model configured
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	live := liveEvents(t, srv, bob)
	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments",
		map[string]any{"speaker": "CUSTOMER", "text": "There is a strange charge, I want a refund", "startSec": 1, "endSec": 2}, nil)
	ev := nextSuggestion(t, live, 5*time.Second)
	if ev == nil || ev.Assist.Model != "" || ev.Assist.Pending || !strings.Contains(ev.Assist.Answer, "3 to 5 business days") {
		t.Fatalf("fallback suggestion = %+v", ev)
	}
}

func TestEssentials(t *testing.T) {
	say, steps := essentials("Block the card straight away. Verify the customer. Ask about fraud. Order a replacement.\n\nSay: \"I'll block your card right now.\"")
	if say != "I'll block your card right now." || len(steps) != 4 || steps[0] != "Block the card straight away." {
		t.Errorf("essentials = %q / %q", say, steps)
	}
	if say, steps := essentials("No sentence end here"); say != "" || len(steps) != 1 || steps[0] != "No sentence end here" {
		t.Errorf("essentials of a fragment = %q / %q", say, steps)
	}
	if got := suggestionText("Hello there.", []string{"Step one."}); got != `Say: "Hello there." Step one.` {
		t.Errorf("suggestionText = %q", got)
	}
}

func TestTidySuggestion(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"Refund the duplicate charge within 3 to 5 days [1].", "Refund the duplicate charge within 3 to 5 days [1].", true},
		{`[1]  "Confirm the charge date and amount, then issue the refund."`, "Confirm the charge date and amount, then issue the refund. [1]", true},
		{"**Offer** the 20% loyalty discount for 12 months [2]", "Offer the 20% loyalty discount for 12 months [2]", true},
		{"[1]", "", false},
		{"  [1] [2] ok  ", "", false},
	} {
		got, ok := tidySuggestion(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("tidySuggestion(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestEmptyModelSuggestionShowsPolicy(t *testing.T) {
	ix := testKB(t)
	w := &fakeWriter{jsonReply: `{"say": "[1]", "steps": []}`} // what Gemma once returned for real
	srv := newTestServer(t, func(o *Options) { o.KB, o.Writer, o.WriterModel = ix, w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	live := liveEvents(t, srv, bob)
	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments",
		map[string]any{"speaker": "CUSTOMER", "text": "I was charged twice and need a refund", "startSec": 1, "endSec": 2}, nil)
	ev := finalSuggestion(t, live, 5*time.Second)
	if ev == nil || ev.Assist.Model != "" || !strings.Contains(ev.Assist.Answer, "3 to 5 business days") {
		t.Fatalf("suggestion = %+v", ev)
	}
}

// Without a policy question the guidance is one line: no steps, no source
func TestGuidanceIsOneLineUnlessAskedForSteps(t *testing.T) {
	ix := testKB(t)
	w := &fakeWriter{jsonReply: `{"say": "I'm sorry about that, I'll look into the double charge now."}`}
	srv := newTestServer(t, func(o *Options) { o.KB, o.Writer, o.WriterModel = ix, w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	live := liveEvents(t, srv, bob)
	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments",
		map[string]any{"speaker": "CUSTOMER", "text": "I was charged twice and need a refund", "startSec": 1, "endSec": 2}, nil)
	ev := finalSuggestion(t, live, 5*time.Second)
	if ev == nil || ev.Assist.Say == "" || len(ev.Assist.Steps) != 0 || len(ev.Assist.Sources) != 0 {
		t.Fatalf("suggestion = %+v", ev)
	}
}

func TestAsksForSteps(t *testing.T) {
	for text, want := range map[string]bool{
		"What is your refund policy?": true, "what are the steps": true, "Is that allowed by the rules": true,
		"I was charged twice": false, "my card is blocked": false,
	} {
		if got := asksForSteps(text); got != want {
			t.Errorf("asksForSteps(%q) = %v", text, got)
		}
	}
}

func TestFirstSentences(t *testing.T) {
	if got := firstSentences("One. Two! Three? Four.", 2); got != "One. Two!" {
		t.Errorf("got %q", got)
	}
	if got := firstSentences("Just one sentence", 2); got != "Just one sentence" {
		t.Errorf("got %q", got)
	}
}

// A short courtesy is a fine reply when no policy is involved
func TestShortCourtesyReplyIsKept(t *testing.T) {
	ix := testKB(t)
	w := &fakeWriter{jsonReply: `{"say": "You're welcome."}`}
	srv := newTestServer(t, func(o *Options) { o.KB, o.Writer, o.WriterModel = ix, w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	live := liveEvents(t, srv, bob)
	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments",
		map[string]any{"speaker": "CUSTOMER", "text": "I am travelling next week for work", "startSec": 1, "endSec": 2}, nil)
	ev := finalSuggestion(t, live, 5*time.Second)
	if ev == nil || ev.Assist.Say != "You're welcome." {
		t.Fatalf("suggestion = %+v", ev)
	}
}
