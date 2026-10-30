package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"lca-backend/internal/asr"
	"lca-backend/internal/store"
)

// fakeDeepgram replays a short conversation once audio arrives: an interim
// result, then a customer utterance (speaker 1), then on CloseStream an
// agent line (speaker 0) before closing.
func fakeDeepgram(t *testing.T) *httptest.Server {
	t.Helper()
	result := func(text string, final, speechFinal bool, start float64, speaker int) string {
		b, _ := json.Marshal(map[string]any{
			"type": "Results", "is_final": final, "speech_final": speechFinal, "start": start, "duration": 2.0,
			"channel": map[string]any{"alternatives": []any{map[string]any{
				"transcript": text, "words": []any{map[string]any{"speaker": speaker}},
			}}},
		})
		return string(b)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		if q.Get("encoding") != "linear16" || q.Get("sample_rate") != "16000" || q.Get("channels") != "1" {
			http.Error(w, "raw audio format not described", http.StatusBadRequest)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		sent := false
		for {
			kind, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if kind == websocket.MessageBinary && !sent {
				sent = true
				for _, m := range []string{
					`{"type":"Metadata"}`,
					`{"type":"SpeechStarted","channel":[0],"timestamp":0.9}`,
					result("I was charged", false, false, 1, 1),
					result("I was charged twice on my card 4111 1111 1111 1111.", true, false, 1, 1),
					result("This is unacceptable!", true, true, 3, 1),
				} {
					_ = c.Write(ctx, websocket.MessageText, []byte(m))
				}
			}
			if kind == websocket.MessageText && strings.Contains(string(data), "CloseStream") {
				_ = c.Write(ctx, websocket.MessageText, []byte(result("I'm sorry, let me fix that.", true, false, 6, 0)))
				_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"UtteranceEnd","channel":[0,1],"last_word_end":8.0}`))
				c.Close(websocket.StatusNormalClosure, "")
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withDeepgram(url string) func(*Options) {
	return func(o *Options) {
		o.ASR = &asr.Client{APIKey: "test-key", Model: asr.DefaultModel, URL: "ws" + strings.TrimPrefix(url, "http")}
	}
}

func dialStream(t *testing.T, srv *httptest.Server, c *http.Client, callID, query string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/stream/" + callID + query
	return websocket.Dial(context.Background(), url, &websocket.DialOptions{HTTPClient: c})
}

func TestStreamAudioThroughDeepgram(t *testing.T) {
	dg := fakeDeepgram(t)
	srv := newTestServer(t, withDeepgram(dg.URL))
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")

	var cfg map[string]string
	res, _ := agent.Get(srv.URL + "/api/stream/config")
	_ = json.NewDecoder(res.Body).Decode(&cfg)
	res.Body.Close()
	if cfg["engine"] != "deepgram" {
		t.Fatalf("engine = %q", cfg["engine"])
	}

	var call store.Call
	postJSON(t, agent, srv.URL+"/api/stream/calls", map[string]string{}, &call)

	conn, _, err := dialStream(t, srv, agent, call.ID, "?lang=en-US&speaker=auto&offset=10&encoding=linear16&sampleRate=16000")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("webm-audio")); err != nil {
		t.Fatal(err)
	}
	stopSent := false
	var interim []string
	var lines []store.Segment
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (got lines %+v)", err, lines)
		}
		var m wsMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		switch m.Type {
		case "interim":
			interim = append(interim, m.Text)
		case "segments":
			lines = append(lines, m.Segments.Segments...)
			if !stopSent {
				stopSent = true
				_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"stop"}`))
			}
		case "error":
			t.Fatalf("error message: %s", m.Message)
		}
		if m.Type == "closed" {
			break
		}
	}

	if len(interim) == 0 || interim[0] != "I was charged" {
		t.Errorf("interim = %q", interim)
	}
	first := lines[0]
	if first.Speaker != "CUSTOMER" || !strings.Contains(first.Text, "[CARD]") || strings.Contains(first.Text, "4111") {
		t.Errorf("first line = %+v", first)
	}
	if !strings.Contains(first.Text, "unacceptable") || first.TimeStart != "00:11.0" || first.TimeEnd != "00:15.0" {
		t.Errorf("utterance not joined or offset wrong: %+v", first)
	}
	last := lines[len(lines)-1]
	if last.Speaker != "AGENT" || last.Text != "I'm sorry, let me fix that." {
		t.Errorf("flushed last line = %+v", last)
	}
	hasTip := false
	for _, l := range lines {
		hasTip = hasTip || l.Speaker == "AGENT_ASSISTANT"
	}
	if !hasTip {
		t.Error("no keyword live-assist tip for the billing complaint")
	}

	saved, _ := postJSONGetCall(t, agent, srv.URL+"/api/calls/"+call.ID)
	if len(saved.Transcript) != len(lines) || saved.AlertCount != 0 {
		t.Errorf("stored transcript %d lines (want %d), alerts during call %d (want 0)", len(saved.Transcript), len(lines), saved.AlertCount)
	}
	// Alerts are alert categories (a manager escalation), as in LCA: an
	// angry billing complaint alone isn't one
	var ended store.Call
	postJSON(t, agent, srv.URL+"/api/stream/calls/"+call.ID+"/end", map[string]any{"durationSec": 10}, &ended)
	if ended.AlertCount != 0 || !slices.Contains(ended.Categories, "Billing") {
		t.Errorf("after the call: alerts = %d (want 0), categories = %q", ended.AlertCount, ended.Categories)
	}
}

func postJSONGetCall(t *testing.T, c *http.Client, url string) (store.Call, int) {
	t.Helper()
	res, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var call store.Call
	_ = json.NewDecoder(res.Body).Decode(&call)
	return call, res.StatusCode
}

func TestStreamAudioGuards(t *testing.T) {
	dg := fakeDeepgram(t)
	srv := newTestServer(t, withDeepgram(dg.URL))
	bob := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	babu := signIn(t, srv, "babu@cxplatform.com", "agent123", "agent")
	var call store.Call
	postJSON(t, bob, srv.URL+"/api/stream/calls", map[string]string{}, &call)

	cases := []struct {
		name   string
		client *http.Client
		query  string
		want   int
	}{
		{"other agent's call", babu, "?lang=en-US", http.StatusNotFound},
		{"bad language", bob, "?lang=en_US;drop", http.StatusBadRequest},
		{"bad speaker", bob, "?lang=en-US&speaker=BOSS", http.StatusBadRequest},
		{"bad offset", bob, "?lang=en-US&offset=-5", http.StatusBadRequest},
		{"bad encoding", bob, "?lang=en-US&encoding=mp3&sampleRate=16000", http.StatusBadRequest},
		{"bad sample rate", bob, "?lang=en-US&encoding=linear16&sampleRate=12345", http.StatusBadRequest},
		{"not signed in", http.DefaultClient, "?lang=en-US", http.StatusUnauthorized},
	}
	for _, c := range cases {
		_, res, err := dialStream(t, srv, c.client, call.ID, c.query)
		if err == nil || res == nil || res.StatusCode != c.want {
			status := 0
			if res != nil {
				status = res.StatusCode
			}
			t.Errorf("%s: status %d, want %d", c.name, status, c.want)
		}
	}

	// Without a key the page is told to use the browser engine, and the socket is refused
	plain := newTestServer(t)
	agent := signIn(t, plain, "agent@cxplatform.com", "agent123", "agent")
	var cfg map[string]string
	res, _ := agent.Get(plain.URL + "/api/stream/config")
	_ = json.NewDecoder(res.Body).Decode(&cfg)
	res.Body.Close()
	if cfg["engine"] != "browser" {
		t.Errorf("engine without key = %q", cfg["engine"])
	}
	var c2 store.Call
	postJSON(t, agent, plain.URL+"/api/stream/calls", map[string]string{}, &c2)
	if _, res, _ := dialStream(t, plain, agent, c2.ID, "?lang=en-US"); res == nil || res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("socket without key should be 503")
	}
}

func TestResolveSpeaker(t *testing.T) {
	if resolveSpeaker("AUTO", 0) != "AGENT" || resolveSpeaker("AUTO", 2) != "CUSTOMER" ||
		resolveSpeaker("AUTO", -1) != "AGENT" || resolveSpeaker("CUSTOMER", 0) != "CUSTOMER" {
		t.Error("resolveSpeaker mismatch")
	}
}

func TestStreamLoudness(t *testing.T) {
	var st streamStats
	if rms, _ := st.loudness(); !math.IsInf(rms, -1) {
		t.Errorf("empty stream rms = %v", rms)
	}
	// Square wave at half scale: RMS and peak both about -6 dBFS
	buf := make([]byte, 0, 200)
	for i := 0; i < 100; i++ {
		v := int16(16384)
		if i%2 == 1 {
			v = -16384
		}
		buf = binary.LittleEndian.AppendUint16(buf, uint16(v))
	}
	st.addPCM(buf)
	rms, peak := st.loudness()
	if rms != -6 || peak != -6 {
		t.Errorf("rms=%v peak=%v, want -6 -6", rms, peak)
	}
}
