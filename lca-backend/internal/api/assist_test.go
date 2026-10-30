package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lca-backend/internal/embed"
	"lca-backend/internal/kb"
	"lca-backend/internal/store"
)

// testKB builds a knowledge base over two documents with a fake embedder
// that maps text to [refund-ness, password-ness, anything-else].
func testKB(t *testing.T) *kb.Index {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := make([][]float32, len(req.Input))
		for i, s := range req.Input {
			s = strings.ToLower(s)
			v := []float32{0, 0, 1} // unrelated text points elsewhere
			if strings.Contains(s, "refund") || strings.Contains(s, "charge") {
				v[0], v[2] = 1, 0
			}
			if strings.Contains(s, "password") {
				v[1], v[2] = 1, 0
			}
			out[i] = v
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "billing.md"), []byte("# Billing\n\n## Duplicate charges\n\nRefunds take 3 to 5 business days."), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "access.md"), []byte("# Access\n\n## Password reset\n\nReset a password from the sign-in page."), 0o644)
	ix := kb.New(dir, embed.NewClient(srv.URL, "fake"), nil)
	if err := ix.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ix
}

func ask(t *testing.T, c *http.Client, url string, body map[string]any) (assistResponse, int) {
	t.Helper()
	var out assistResponse
	st := postJSON(t, c, url+"/api/assist", body, &out)
	return out, st
}

func TestAssistKnowledgeBase(t *testing.T) {
	ix := testKB(t)
	srv := newTestServer(t, func(o *Options) { o.KB = ix })
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")

	res, st := ask(t, agent, srv.URL, map[string]any{"question": "How long does a refund take?"})
	if st != http.StatusOK || !strings.Contains(res.Answer, "3 to 5 business days") || !strings.HasPrefix(res.Answer, "Duplicate charges: ") {
		t.Errorf("answer = %q (%d)", res.Answer, st)
	}
	if len(res.Sources) == 0 || res.Sources[0].File != "billing.md" || res.Sources[0].Title != "Billing" {
		t.Errorf("sources = %+v", res.Sources)
	}

	res, _ = ask(t, agent, srv.URL, map[string]any{"question": "What is the weather today?"})
	if !strings.Contains(res.Answer, "couldn't find") || len(res.Sources) != 0 {
		t.Errorf("unrelated question answer = %q sources=%v", res.Answer, res.Sources)
	}

	if _, st := ask(t, agent, srv.URL, map[string]any{"question": strings.Repeat("x", maxQuestionChars+1)}); st != http.StatusBadRequest {
		t.Errorf("long question status = %d", st)
	}
	if _, st := ask(t, agent, srv.URL, map[string]any{"action": "dance"}); st != http.StatusBadRequest {
		t.Errorf("bad action status = %d", st)
	}

	var status kb.Status
	res2, _ := agent.Get(srv.URL + "/api/assist/status")
	_ = json.NewDecoder(res2.Body).Decode(&status)
	res2.Body.Close()
	if !status.Ready || status.Documents != 2 || status.Model != "fake" {
		t.Errorf("status = %+v", status)
	}
	// Only admins may rebuild
	if st := postJSON(t, agent, srv.URL+"/api/assist/reload", map[string]any{}, nil); st != http.StatusForbidden {
		t.Errorf("agent reload status = %d", st)
	}
}

func TestAssistCallActions(t *testing.T) {
	ix := testKB(t)
	srv := newTestServer(t, func(o *Options) { o.KB = ix })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	babu := signIn(t, srv, "babu@cxplatform.com", "agent123", "agent")

	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	for _, l := range []map[string]any{
		{"speaker": "CUSTOMER", "text": "I was charged twice, I need a refund.", "startSec": 1, "endSec": 3},
		{"speaker": "AGENT", "text": "I've refunded the duplicate charge.", "startSec": 4, "endSec": 6},
	} {
		postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments", l, nil)
	}

	res, _ := ask(t, bob, srv.URL, map[string]any{"action": "summary", "callId": call.ID})
	if !strings.Contains(res.Answer, "I was charged twice") || strings.Contains(res.Answer, "**") {
		t.Errorf("summary = %q", res.Answer)
	}
	res, _ = ask(t, bob, srv.URL, map[string]any{"action": "topic", "callId": call.ID})
	if !strings.Contains(res.Answer, "Current topic: Billing") || !strings.Contains(res.Answer, "3 to 5 business days") || len(res.Sources) == 0 {
		t.Errorf("topic = %q sources=%v", res.Answer, res.Sources)
	}
	res, _ = ask(t, bob, srv.URL, map[string]any{"action": "email", "callId": call.ID})
	if !strings.HasPrefix(res.Answer, "Subject: ") || !strings.Contains(res.Answer, "about billing.") ||
		!strings.Contains(res.Answer, "I've refunded the duplicate charge.") || !strings.Contains(res.Answer, "BobS") {
		t.Errorf("email = %q", res.Answer)
	}
	if readableCategory("ManagerEscalation") != "manager escalation" || readableCategory("HowCanIHelp") != "general enquiry" {
		t.Error("readableCategory")
	}
	res, _ = ask(t, bob, srv.URL, map[string]any{"action": "summary"})
	if !strings.Contains(res.Answer, "Open or answer a call first") {
		t.Errorf("no call answer = %q", res.Answer)
	}
	if _, st := ask(t, babu, srv.URL, map[string]any{"action": "summary", "callId": call.ID}); st != http.StatusNotFound {
		t.Errorf("other agent's call status = %d, want 404", st)
	}
}

func TestAssistCallActionsWithModel(t *testing.T) {
	ix := testKB(t)
	w := &fakeWriter{}
	srv := newTestServer(t, func(o *Options) { o.KB, o.Writer, o.WriterModel = ix, w, "gemma2:2b" })
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")

	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)
	for _, l := range []map[string]any{
		{"speaker": "CUSTOMER", "text": "I was charged twice on card 4111 1111 1111 1111, I need a refund.", "startSec": 1, "endSec": 3},
		{"speaker": "AGENT", "text": "I've refunded the duplicate charge.", "startSec": 4, "endSec": 6},
	} {
		postJSON(t, bob, srv.URL+"/api/stream/calls/"+call.ID+"/segments", l, nil)
	}

	w.reply = "Reason for call: **double charge**\n- Agent refunded it\nNext step: None yet"
	res, _ := ask(t, bob, srv.URL, map[string]any{"action": "summary", "callId": call.ID})
	if res.Answer != "Reason for call: double charge\n- Agent refunded it\nNext step: None yet" || res.Model != "gemma2:2b" {
		t.Errorf("summary = %+v", res)
	}
	if !strings.Contains(w.prompt, "CUSTOMER: I was charged twice on card [CARD]") || !strings.Contains(w.prompt, "AGENT: I've refunded") ||
		strings.Contains(w.prompt, "4111") || strings.Contains(w.prompt, "AGENT_ASSISTANT") {
		t.Errorf("summary prompt:\n%s", w.prompt)
	}

	w.reply = "Topic: duplicate charge refund\nCustomer wants: their money back"
	res, _ = ask(t, bob, srv.URL, map[string]any{"action": "topic", "callId": call.ID})
	if !strings.HasPrefix(res.Answer, w.reply) || !strings.Contains(res.Answer, "Related guidance (Billing)") || res.Model != "gemma2:2b" || len(res.Sources) == 0 {
		t.Errorf("topic = %+v", res)
	}

	w.reply = "Subject: Your refund\n\nHello,\nWe refunded the duplicate charge.\n\nKind regards,\nBobS\nCustomer Support"
	res, _ = ask(t, bob, srv.URL, map[string]any{"action": "email", "callId": call.ID})
	if res.Answer != w.reply || !strings.Contains(w.prompt, "Kind regards,\nBobS\nCustomer Support") {
		t.Errorf("email = %+v\nprompt:\n%s", res, w.prompt)
	}

	// Model down: the rule-based versions are used
	w.reply, w.err = "", errors.New("ollama not running")
	res, _ = ask(t, bob, srv.URL, map[string]any{"action": "email", "callId": call.ID})
	if !strings.Contains(res.Answer, "about billing.") || res.Model != "" {
		t.Errorf("fallback email = %+v", res)
	}
	res, _ = ask(t, bob, srv.URL, map[string]any{"action": "topic", "callId": call.ID})
	if !strings.Contains(res.Answer, "Current topic: Billing") || res.Model != "" {
		t.Errorf("fallback topic = %+v", res)
	}
}

// Real Gemma 2 replies that needed tidying
func TestTidyEmailAndTopic(t *testing.T) {
	for in, want := range map[string]string{
		"Subject: \n\nHello,\nThanks.":       "Subject: Follow-up on your call with us\n\nHello,\nThanks.",
		"Subject: Your refund\n\nHello,":     "Subject: Your refund\n\nHello,",
		"Hello,\nThanks.":                    "Subject: Follow-up on your call with us\n\nHello,\nThanks.",
		"**Subject:** Your refund\n\nHello,": "Subject: Your refund\n\nHello,",
	} {
		if got := tidyEmail(in); got != want {
			t.Errorf("tidyEmail(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Topic:  Internet bill billing issue\nCustomer wants refund for duplicate charge.": "Topic: Internet bill billing issue\nCustomer wants: refund for duplicate charge.",
		"Topic: Refund\n\nCustomer wants: money back":                                      "Topic: Refund\nCustomer wants: money back",
	} {
		if got := tidyTopic(in); got != want {
			t.Errorf("tidyTopic(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTranscriptPromptKeepsRecentLines(t *testing.T) {
	var call store.Call
	for i := range 400 {
		call.Transcript = append(call.Transcript, store.Segment{Speaker: "CUSTOMER", Text: fmt.Sprintf("line %d with some padding text", i)})
	}
	p := transcriptPrompt(call, 0)
	if len(p) > maxPromptTranscriptChars+len("Transcript:\n") || !strings.HasSuffix(p, "line 399 with some padding text") ||
		!strings.HasPrefix(p, "Transcript:\nCUSTOMER: line ") {
		t.Errorf("long prompt: %d chars, starts %q", len(p), p[:40])
	}
	if p := transcriptPrompt(call, 2); p != "Transcript:\nCUSTOMER: line 398 with some padding text\nCUSTOMER: line 399 with some padding text" {
		t.Errorf("last 2 lines = %q", p)
	}
}

func TestAssistWithoutKnowledgeBase(t *testing.T) {
	srv := newTestServer(t)
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	if _, st := ask(t, agent, srv.URL, map[string]any{"question": "refund?"}); st != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", st)
	}
}

// fakeWriter stands in for Gemma: it records the prompt and returns a canned reply.
type fakeWriter struct {
	mu     sync.Mutex // the post-call analysis calls it in the background
	reply  string
	err    error
	prompt string
	// Reply to ChatJSON; empty uses reply
	jsonReply string
}

func (f *fakeWriter) Chat(_ context.Context, system, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompt = system + "\n---\n" + user
	return f.reply, f.err
}

func (f *fakeWriter) ChatJSON(_ context.Context, system, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompt = system + "\n---\n" + user
	if f.jsonReply != "" {
		return f.jsonReply, f.err
	}
	return f.reply, f.err
}

// lastPrompt reads the prompt safely while background work may be running.
func (f *fakeWriter) lastPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompt
}

func TestAssistGeneratesAnswerWithModel(t *testing.T) {
	ix := testKB(t)
	w := &fakeWriter{reply: "Refunds reach the card in 3 to 5 business days [1]."}
	srv := newTestServer(t, func(o *Options) { o.KB, o.Writer, o.WriterModel = ix, w, "gemma2:2b" })
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")

	res, _ := ask(t, agent, srv.URL, map[string]any{"question": "Refund for charge on card 4111 1111 1111 1111?"})
	if res.Answer != w.reply || res.Model != "gemma2:2b" || len(res.Sources) == 0 {
		t.Errorf("answer = %+v", res)
	}
	if !strings.Contains(w.prompt, "[1] Billing - Duplicate charges: Refunds take 3 to 5 business days.") || !strings.Contains(w.prompt, "ONLY the numbered passages") {
		t.Errorf("prompt missing passages or instructions:\n%s", w.prompt)
	}
	if strings.Contains(w.prompt, "4111") || !strings.Contains(w.prompt, "[CARD]") {
		t.Errorf("card number reached the model:\n%s", w.prompt)
	}

	// The model says the passages don't answer it
	w.reply = "NOT_FOUND"
	res, _ = ask(t, agent, srv.URL, map[string]any{"question": "Can I get a refund in cash?"})
	if !strings.Contains(res.Answer, "couldn't find") || len(res.Sources) != 0 || res.Model != "" {
		t.Errorf("NOT_FOUND answer = %+v", res)
	}

	// Model down: the best passage is quoted instead
	w.reply, w.err = "", errors.New("ollama not running")
	res, _ = ask(t, agent, srv.URL, map[string]any{"question": "How long does a refund take?"})
	if !strings.Contains(res.Answer, "3 to 5 business days") || res.Model != "" || len(res.Sources) == 0 {
		t.Errorf("fallback answer = %+v", res)
	}

	// Unrelated questions never reach the model
	w.prompt = ""
	res, _ = ask(t, agent, srv.URL, map[string]any{"question": "What is the weather?"})
	if w.prompt != "" || !strings.Contains(res.Answer, "couldn't find") {
		t.Errorf("unrelated question was sent to the model: %q", w.prompt)
	}
}
