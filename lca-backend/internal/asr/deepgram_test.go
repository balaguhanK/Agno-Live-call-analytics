package asr

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestParse(t *testing.T) {
	ev, ok, err := parse([]byte(`{"type":"Results","is_final":true,"speech_final":false,"start":1.5,"duration":2,
		"channel":{"alternatives":[{"transcript":" Hello there. ","words":[{"speaker":1},{"speaker":1},{"speaker":0}]}]}}`))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	tr := ev.Transcript
	if tr.Text != "Hello there." || !tr.IsFinal || tr.SpeechFinal || tr.Start != 1.5 || tr.End != 3.5 || tr.Speaker != 1 {
		t.Errorf("transcript = %+v", tr)
	}
	// Real UtteranceEnd payload: "channel" is a list here, unlike in Results
	if ev, ok, err := parse([]byte(`{"type":"UtteranceEnd","channel":[0,1],"last_word_end":3.1}`)); err != nil || !ok || !ev.UtteranceEnd {
		t.Errorf("UtteranceEnd not reported: %v", err)
	}
	if _, ok, err := parse([]byte(`{"type":"SpeechStarted","channel":[0],"timestamp":0.5}`)); ok || err != nil {
		t.Errorf("SpeechStarted should be skipped quietly: %v", err)
	}
	if _, ok, _ := parse([]byte(`{"type":"Metadata"}`)); ok {
		t.Error("Metadata should be skipped")
	}
}

func TestAssembler(t *testing.T) {
	var a Assembler
	if p, done := a.Add(Transcript{Text: "I was", Speaker: 1}); p != "I was" || done != nil {
		t.Fatalf("interim: %q %v", p, done)
	}
	if p, _ := a.Add(Transcript{Text: "I was charged twice.", IsFinal: true, Start: 0, End: 2, Speaker: 1}); p != "I was charged twice." {
		t.Fatalf("after final piece: %q", p)
	}
	if p, _ := a.Add(Transcript{Text: "It's", Speaker: 1}); p != "I was charged twice. It's" {
		t.Fatalf("preview: %q", p)
	}
	_, done := a.Add(Transcript{Text: "It's wrong.", IsFinal: true, SpeechFinal: true, Start: 2, End: 3, Speaker: 0})
	if done == nil || done.Text != "I was charged twice. It's wrong." || done.Start != 0 || done.End != 3 || done.Speaker != 1 {
		t.Fatalf("utterance = %+v", done)
	}
	if a.Flush() != nil {
		t.Error("assembler not reset")
	}
}

func TestOpenReportsBadKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"err_code":"INVALID_AUTH","err_msg":"Invalid credentials."}`))
	}))
	defer srv.Close()
	c := &Client{APIKey: "bad", Model: DefaultModel, URL: "ws" + strings.TrimPrefix(srv.URL, "http")}
	_, err := c.Open(context.Background(), StreamOptions{Language: "en-US"})
	if err == nil || !strings.Contains(err.Error(), "DEEPGRAM_API_KEY") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret" || r.URL.Query().Get("model") != "nova-3" ||
			r.URL.Query().Get("language") != "en-GB" || r.URL.Query().Get("diarize") != "true" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		for {
			kind, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if kind == websocket.MessageBinary {
				_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"Results","is_final":true,"speech_final":true,"start":0,"duration":1,
					"channel":{"alternatives":[{"transcript":"got `+string(data)+`","words":[]}]}}`))
			} else if strings.Contains(string(data), "CloseStream") {
				c.Close(websocket.StatusNormalClosure, "")
				return
			}
		}
	}))
	defer srv.Close()

	c := &Client{APIKey: "secret", Model: DefaultModel, URL: "ws" + strings.TrimPrefix(srv.URL, "http")}
	ctx := context.Background()
	st, err := c.Open(ctx, StreamOptions{Language: "en-GB"})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SendAudio(ctx, []byte("audio")); err != nil {
		t.Fatal(err)
	}
	ev, err := st.Next(ctx)
	if err != nil || ev.Transcript == nil || ev.Transcript.Text != "got audio" || ev.Transcript.Speaker != -1 {
		t.Fatalf("event = %+v, %v", ev.Transcript, err)
	}
	if err := st.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Next(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("after finish err = %v, want EOF", err)
	}
}

func TestTranscribeFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Token secret" || r.Header.Get("Content-Type") != "audio/webm" ||
			string(body) != "clip" || r.URL.Query().Get("language") != "en-US" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"metadata":{"duration":4.02,"channels":1},
			"results":{"channels":[{"alternatives":[{"transcript":"testing one two","confidence":0.97}]}]}}`))
	}))
	defer srv.Close()
	c := &Client{APIKey: "secret", Model: DefaultModel, URL: "ws" + strings.TrimPrefix(srv.URL, "http")}
	res, err := c.TranscribeFile(context.Background(), []byte("clip"), "audio/webm", "en-US")
	if err != nil || res.Transcript != "testing one two" || res.Duration != 4.02 || res.Channels != 1 {
		t.Fatalf("res = %+v, %v", res, err)
	}
}
