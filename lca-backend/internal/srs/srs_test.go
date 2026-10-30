package srs

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"lca-backend/internal/asr"
	"lca-backend/internal/siprec"
	"lca-backend/internal/srctest"
)

// fakeDeepgram answers each stream with one utterance naming the first μ-law
// byte it received, so the test can tell which participant's audio it was.
func fakeDeepgram(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("encoding") != "mulaw" || q.Get("sample_rate") != "8000" {
			http.Error(w, "expected 8 kHz mulaw", http.StatusBadRequest)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		answered := false
		for {
			kind, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if kind == websocket.MessageBinary && !answered && len(data) > 0 {
				answered = true
				msg := fmt.Sprintf(`{"type":"Results","is_final":true,"speech_final":true,"start":0.5,"duration":1,
					"channel":{"alternatives":[{"transcript":"marker %02x","words":[]}]}}`, data[0])
				_ = c.Write(ctx, websocket.MessageText, []byte(msg))
			}
			if kind == websocket.MessageText && strings.Contains(string(data), "CloseStream") {
				c.Close(websocket.StatusNormalClosure, "")
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type fakeBackend struct {
	mu     sync.Mutex
	call   map[string]string
	lines  []map[string]any
	ended  chan float64
	server *httptest.Server
}

func newFakeBackend(t *testing.T) *fakeBackend {
	t.Helper()
	b := &fakeBackend{ended: make(chan float64, 1)}
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer ingest-token" {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/ingest/agents", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"agents":["BobS","BabuS"]}`))
	}))
	mux.HandleFunc("POST /api/ingest/calls", auth(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&b.call)
		b.mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"call-1"}`))
	}))
	mux.HandleFunc("POST /api/ingest/calls/call-1/lines", auth(func(w http.ResponseWriter, r *http.Request) {
		var line map[string]any
		_ = json.NewDecoder(r.Body).Decode(&line)
		b.mu.Lock()
		b.lines = append(b.lines, line)
		b.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	mux.HandleFunc("POST /api/ingest/calls/call-1/end", auth(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DurationSec float64 `json:"durationSec"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		b.ended <- body.DurationSec
	}))
	b.server = httptest.NewServer(mux)
	t.Cleanup(b.server.Close)
	return b
}

// tone returns n samples of a constant level, which encodes to one μ-law byte.
func tone(level int16, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = level
	}
	return out
}

func TestRecordingSessionEndToEnd(t *testing.T) {
	dg := fakeDeepgram(t)
	backend := newFakeBackend(t)

	srv, err := New(Config{MediaIP: "127.0.0.1", RTPPortMin: 42000, RTPPortMax: 42100, Language: "en-US"},
		&asr.Client{APIKey: "k", Model: asr.DefaultModel, URL: "ws" + strings.TrimPrefix(dg.URL, "http")},
		&Backend{BaseURL: backend.server.URL, Token: "ingest-token", HTTP: http.DefaultClient})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := net.Listen("tcp", conn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, conn, tcp) }()
	t.Cleanup(func() { cancel(); <-served })

	customerAudio, agentAudio := tone(2000, 4000), tone(-2000, 4000) // 0.5 s each
	callCtx, callCancel := context.WithTimeout(ctx, 15*time.Second)
	defer callCancel()
	err = srctest.Run(callCtx, srctest.Call{
		SRS:     conn.LocalAddr().String(),
		LocalIP: "127.0.0.1",
		Parties: []srctest.Party{
			{AOR: "tel:+17035550100", Name: "Caller", Audio: customerAudio},
			{AOR: "sip:bobs@pbx.example", Name: "Bob Smith", Audio: agentAudio},
		},
	})
	if err != nil {
		t.Fatalf("SRC: %v", err)
	}

	select {
	case dur := <-backend.ended:
		if dur <= 0 {
			t.Errorf("duration = %v", dur)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("call was never ended")
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.call["agent"] != "BobS" || backend.call["callerPhoneNumber"] != "+17035550100" || backend.call["systemPhoneNumber"] != "SIPREC" {
		t.Errorf("created call = %v", backend.call)
	}
	want := map[string]string{
		"CUSTOMER": fmt.Sprintf("marker %02x", siprec.MulawEncode(2000)),
		"AGENT":    fmt.Sprintf("marker %02x", siprec.MulawEncode(-2000)),
	}
	got := map[string]string{}
	for _, l := range backend.lines {
		got[l["speaker"].(string)] = l["text"].(string)
	}
	for speaker, text := range want {
		if got[speaker] != text {
			t.Errorf("%s line = %q, want %q (all lines %v)", speaker, got[speaker], text, backend.lines)
		}
	}
}

func TestAssignSpeakers(t *testing.T) {
	sdp := siprec.SDP{Streams: []siprec.MediaStream{{Label: "1"}, {Label: "2"}}}
	meta := func(aors ...string) *siprec.Metadata {
		parts := make([]siprec.Participant, len(aors))
		for i, a := range aors {
			parts[i] = siprec.Participant{AOR: a, Label: fmt.Sprint(i + 1)}
		}
		var m siprec.Metadata
		if err := xmlUnmarshal(siprec.BuildMetadata("s1", parts), &m); err != nil {
			t.Fatal(err)
		}
		return &m
	}

	// Agent found by SIP user, even when listed first
	r := assignSpeakers(sdp, meta("sip:BabuS@pbx", "tel:+15550001"), []string{"BobS", "BabuS"})
	if r.agent != "BabuS" || r.caller != "+15550001" || r.speakers["1"] != "AGENT" || r.speakers["2"] != "CUSTOMER" {
		t.Errorf("matched: %+v", r)
	}
	// No match: caller first, agent second
	r = assignSpeakers(sdp, meta("tel:+15550001", "sip:1001@pbx"), []string{"BobS"})
	if r.agent != "1001" || r.speakers["1"] != "CUSTOMER" || r.speakers["2"] != "AGENT" {
		t.Errorf("fallback: %+v", r)
	}
	// No metadata at all
	r = assignSpeakers(sdp, nil, nil)
	if r.speakers["1"] != "CUSTOMER" || r.speakers["2"] != "AGENT" {
		t.Errorf("no metadata: %+v", r)
	}
}

func xmlUnmarshal(data []byte, v any) error { return xml.Unmarshal(data, v) }
