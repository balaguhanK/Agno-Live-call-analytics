// Package asr streams audio to Deepgram's live speech-to-text API and turns
// its results into whole utterances.
package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/coder/websocket"
)

const (
	DefaultURL   = "wss://api.deepgram.com/v1/listen"
	DefaultModel = "nova-3"
	// Silence (ms) after which Deepgram finalizes a phrase
	endpointingMs = "300"
	// Silence (ms) after which Deepgram reports the utterance has ended
	utteranceEndMs = "1000"
	maxResultBytes = 1 << 20
)

// Client opens live transcription streams. The API key never leaves the server.
type Client struct {
	APIKey string
	Model  string // e.g. nova-3
	// Words to favour (nova-3 only): names and domain terms are heard right more often
	Keyterms []string
	URL      string // DefaultURL unless testing
}

func NewClient(apiKey, model string) *Client {
	if model == "" {
		model = DefaultModel
	}
	return &Client{APIKey: apiKey, Model: model, URL: DefaultURL}
}

// Transcript is one Deepgram result: interim text, or a finalized piece.
type Transcript struct {
	Text        string
	IsFinal     bool    // this piece won't change
	SpeechFinal bool    // the speaker paused; the utterance is complete
	Start, End  float64 // seconds from stream start
	Speaker     int     // diarized speaker number, -1 if unknown
}

// Event is a parsed Deepgram message. Exactly one field is set.
type Event struct {
	Transcript   *Transcript
	UtteranceEnd bool
}

type Stream struct {
	conn *websocket.Conn
}

// StreamOptions describe the audio. With Encoding empty, Deepgram detects a
// container format (WebM/Opus, WAV); raw audio needs Encoding and SampleRate.
type StreamOptions struct {
	Language   string
	Encoding   string // "" or "linear16" (16-bit little-endian PCM)
	SampleRate int
}

// Open starts a live stream.
func (c *Client) Open(ctx context.Context, opts StreamOptions) (*Stream, error) {
	q := c.streamQuery(opts)
	conn, res, err := websocket.Dial(ctx, c.URL+"?"+q.Encode(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Token " + c.APIKey}},
	})
	if err != nil {
		return nil, dialError(res, err)
	}
	conn.SetReadLimit(maxResultBytes)
	return &Stream{conn: conn}, nil
}

func (c *Client) streamQuery(opts StreamOptions) url.Values {
	language := opts.Language
	q := url.Values{
		"model":            {c.Model},
		"language":         {language},
		"punctuate":        {"true"},
		"smart_format":     {"true"},
		"interim_results":  {"true"},
		"diarize":          {"true"},
		"endpointing":      {endpointingMs},
		"utterance_end_ms": {utteranceEndMs},
		"vad_events":       {"true"},
	}
	if opts.Encoding != "" {
		q.Set("encoding", opts.Encoding)
		q.Set("sample_rate", strconv.Itoa(opts.SampleRate))
		q.Set("channels", "1")
	}
	c.addKeyterms(q)
	return q
}

// addKeyterms adds the words to favour; only nova-3 accepts them.
func (c *Client) addKeyterms(q url.Values) {
	if !strings.HasPrefix(c.Model, "nova-3") {
		return
	}
	for _, t := range c.Keyterms {
		q.Add("keyterm", t)
	}
}

// dialError explains a rejected handshake, e.g. a bad key or unsupported language.
func dialError(res *http.Response, err error) error {
	if res == nil {
		return fmt.Errorf("can't reach Deepgram: %w", err)
	}
	defer res.Body.Close()
	var body struct {
		Message string `json:"err_msg"`
		Reason  string `json:"reason"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	_ = json.Unmarshal(raw, &body)
	detail := strings.TrimSpace(body.Message + " " + body.Reason)
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("Deepgram rejected the API key (%d). Check DEEPGRAM_API_KEY", res.StatusCode)
	case http.StatusPaymentRequired:
		return errors.New("Deepgram account is out of credit")
	}
	if detail == "" {
		detail = res.Status
	}
	return fmt.Errorf("Deepgram refused the stream: %s", detail)
}

func (s *Stream) SendAudio(ctx context.Context, chunk []byte) error {
	return s.conn.Write(ctx, websocket.MessageBinary, chunk)
}

func (s *Stream) control(ctx context.Context, kind string) error {
	return s.conn.Write(ctx, websocket.MessageText, []byte(`{"type":"`+kind+`"}`))
}

// KeepAlive stops Deepgram closing the stream during long silences.
func (s *Stream) KeepAlive(ctx context.Context) error { return s.control(ctx, "KeepAlive") }

// Finish asks Deepgram to transcribe what it has and then close; keep calling
// Next until it returns io.EOF to receive the last results.
func (s *Stream) Finish(ctx context.Context) error { return s.control(ctx, "CloseStream") }

func (s *Stream) Close() { s.conn.CloseNow() }

type result struct {
	Type        string  `json:"type"`
	IsFinal     bool    `json:"is_final"`
	SpeechFinal bool    `json:"speech_final"`
	Start       float64 `json:"start"`
	Duration    float64 `json:"duration"`
	Channel     struct {
		Alternatives []struct {
			Transcript string `json:"transcript"`
			Words      []struct {
				Speaker *int `json:"speaker"`
			} `json:"words"`
		} `json:"alternatives"`
	} `json:"channel"`
}

// Next returns the next transcript or utterance-end event, skipping other
// messages. It returns io.EOF once Deepgram closes the stream normally.
func (s *Stream) Next(ctx context.Context) (Event, error) {
	for {
		_, data, err := s.conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return Event{}, io.EOF
			}
			return Event{}, err
		}
		ev, ok, err := parse(data)
		if err != nil {
			// One odd message shouldn't end the call; skip it
			slog.Warn("skipping Deepgram message", "err", err)
			continue
		}
		if ok {
			return ev, nil
		}
	}
}

// parse reads the message type first: other types reuse field names with
// different shapes (UtteranceEnd's "channel" is a list, not an object).
func parse(data []byte) (Event, bool, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Event{}, false, fmt.Errorf("unreadable Deepgram message: %w", err)
	}
	switch head.Type {
	case "UtteranceEnd":
		return Event{UtteranceEnd: true}, true, nil
	case "Results":
		var r result
		if err := json.Unmarshal(data, &r); err != nil {
			return Event{}, false, fmt.Errorf("unreadable Deepgram result: %w", err)
		}
		t := &Transcript{IsFinal: r.IsFinal, SpeechFinal: r.SpeechFinal, Start: r.Start, End: r.Start + r.Duration, Speaker: -1}
		if len(r.Channel.Alternatives) > 0 {
			alt := r.Channel.Alternatives[0]
			t.Text = strings.TrimSpace(alt.Transcript)
			t.Speaker = mostCommonSpeaker(alt.Words)
		}
		return Event{Transcript: t}, true, nil
	}
	return Event{}, false, nil // Metadata, SpeechStarted, …
}

func mostCommonSpeaker(words []struct {
	Speaker *int `json:"speaker"`
}) int {
	counts := map[int]int{}
	best, bestN := -1, 0
	for _, w := range words {
		if w.Speaker == nil {
			continue
		}
		counts[*w.Speaker]++
		if n := counts[*w.Speaker]; n > bestN {
			best, bestN = *w.Speaker, n
		}
	}
	return best
}

// Utterance is a complete spoken sentence or phrase.
type Utterance struct {
	Text       string
	Start, End float64
	Speaker    int // -1 if unknown
}

// Assembler joins Deepgram's finalized pieces into utterances.
type Assembler struct {
	parts []Transcript
}

// Add takes one transcript. It returns the live preview text (finalized
// pieces so far plus the interim one) and, when the speaker paused, the
// finished utterance.
func (a *Assembler) Add(t Transcript) (preview string, done *Utterance) {
	if t.IsFinal && t.Text != "" {
		a.parts = append(a.parts, t)
	}
	if t.IsFinal && t.SpeechFinal {
		return "", a.Flush()
	}
	texts := make([]string, 0, len(a.parts)+1)
	for _, p := range a.parts {
		texts = append(texts, p.Text)
	}
	if !t.IsFinal && t.Text != "" {
		texts = append(texts, t.Text)
	}
	return strings.Join(texts, " "), nil
}

// Flush returns the pending utterance, or nil if nothing was said.
func (a *Assembler) Flush() *Utterance {
	if len(a.parts) == 0 {
		return nil
	}
	u := &Utterance{Start: a.parts[0].Start, End: a.parts[len(a.parts)-1].End, Speaker: -1}
	texts := make([]string, len(a.parts))
	counts := map[int]float64{}
	for i, p := range a.parts {
		texts[i] = p.Text
		if p.Speaker >= 0 {
			counts[p.Speaker] += p.End - p.Start
		}
	}
	var most float64
	for sp, d := range counts {
		if d > most || (d == most && sp < u.Speaker) || u.Speaker == -1 {
			u.Speaker, most = sp, d
		}
	}
	u.Text = strings.Join(texts, " ")
	a.parts = nil
	return u
}

// FileResult is Deepgram's answer for a short pre-recorded clip.
type FileResult struct {
	Transcript string  `json:"transcript"`
	Confidence float64 `json:"confidence"`
	Duration   float64 `json:"duration"` // seconds of audio Deepgram decoded
	Channels   int     `json:"channels"`
}

// TranscribeFile sends one short recording to Deepgram's pre-recorded API.
// Used by the microphone test to compare against live streaming.
func (c *Client) TranscribeFile(ctx context.Context, audio []byte, contentType, language string) (FileResult, error) {
	q := url.Values{"model": {c.Model}, "language": {language}, "punctuate": {"true"}, "smart_format": {"true"}}
	c.addKeyterms(q)
	endpoint := strings.Replace(strings.Replace(c.URL, "wss://", "https://", 1), "ws://", "http://", 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?"+q.Encode(), bytes.NewReader(audio))
	if err != nil {
		return FileResult{}, err
	}
	req.Header.Set("Authorization", "Token "+c.APIKey)
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return FileResult{}, fmt.Errorf("can't reach Deepgram: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return FileResult{}, dialError(res, errors.New(res.Status))
	}
	defer res.Body.Close()
	var body struct {
		Metadata struct {
			Duration float64 `json:"duration"`
			Channels int     `json:"channels"`
		} `json:"metadata"`
		Results struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string  `json:"transcript"`
					Confidence float64 `json:"confidence"`
				} `json:"alternatives"`
			} `json:"channels"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResultBytes)).Decode(&body); err != nil {
		return FileResult{}, fmt.Errorf("unreadable Deepgram response: %w", err)
	}
	out := FileResult{Duration: body.Metadata.Duration, Channels: body.Metadata.Channels}
	if len(body.Results.Channels) > 0 && len(body.Results.Channels[0].Alternatives) > 0 {
		alt := body.Results.Channels[0].Alternatives[0]
		out.Transcript, out.Confidence = alt.Transcript, alt.Confidence
	}
	return out, nil
}
