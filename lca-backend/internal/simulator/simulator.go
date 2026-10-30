// Package simulator plays scripted calls into the backend's ingest API, so the
// dashboard shows live calls, captions and sentiment without a phone system.
package simulator

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"lca-backend/internal/store"
)

const (
	// Segment scores at or beyond these map to positive / negative labels
	positiveScore = 1.0
	negativeScore = -1.0
	// A caller line at or below this score raises an alert
	alertScore = -2.5
	// The latest score must move this far from the running average to count as a trend
	trendDelta = 0.5

	speakerCustomer = "CUSTOMER"
	speakerAgent    = "AGENT"
)

//go:embed scripts.json
var scriptsJSON []byte

type Turn struct {
	At      float64  `json:"at"` // seconds from call start
	Speaker string   `json:"speaker"`
	Text    string   `json:"text"`
	Score   *float64 `json:"score,omitempty"` // nil for assistant suggestions
	Emoji   *string  `json:"emoji,omitempty"`
	PII     []string `json:"pii,omitempty"`
}

type Script struct {
	Name       string   `json:"name"`
	Categories []string `json:"categories"`
	Summary    string   `json:"summary"`
	Turns      []Turn   `json:"turns"`
}

// Scripts returns the built-in demo conversations.
func Scripts() ([]Script, error) {
	var s []Script
	if err := json.Unmarshal(scriptsJSON, &s); err != nil {
		return nil, fmt.Errorf("parse scripts: %w", err)
	}
	return s, nil
}

// Client talks to the backend's /api/ingest endpoints.
type Client struct {
	BaseURL string // e.g. http://localhost:8090
	Token   string
	HTTP    *http.Client
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/ingest"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("%s %s: %s: %s", method, path, res.Status, bytes.TrimSpace(msg))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Player runs one script as a live call.
type Player struct {
	Client *Client
	Speed  float64 // 1 is real time, 2 is twice as fast
	// Sleep waits between turns; tests replace it to run instantly
	Sleep func(ctx context.Context, d time.Duration) error
	Now   func() time.Time
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Play creates a call for agent, streams the script's turns, then marks the
// call Done. If ctx is cancelled mid-call, the call is still closed.
func (p *Player) Play(ctx context.Context, agent string, s Script) (string, error) {
	sleep, now := p.Sleep, p.Now
	if sleep == nil {
		sleep = sleepCtx
	}
	if now == nil {
		now = time.Now
	}
	speed := p.Speed
	if speed <= 0 {
		speed = 1
	}

	var call store.Call
	err := p.Client.do(ctx, http.MethodPost, "/calls", store.NewCall{
		Agent:               agent,
		InitiationTimestamp: now().UTC().Format(time.RFC3339Nano),
		CallerPhoneNumber:   fakeNumber(),
		SystemPhoneNumber:   "+18005550100",
	}, &call)
	if err != nil {
		return "", err
	}

	st := &callState{}
	var elapsed float64
	for _, t := range s.Turns {
		if err := sleep(ctx, time.Duration((t.At-elapsed)/speed*float64(time.Second))); err != nil {
			return call.ID, p.finish(call.ID, st, s, elapsed)
		}
		elapsed = t.At
		if err := p.Client.do(ctx, http.MethodPost, "/calls/"+call.ID+"/segments", st.segment(t), nil); err != nil {
			return call.ID, err
		}
		if t.Score == nil {
			continue
		}
		update := st.apply(t)
		if err := p.Client.do(ctx, http.MethodPatch, "/calls/"+call.ID, update, nil); err != nil {
			return call.ID, err
		}
	}
	return call.ID, p.finish(call.ID, st, s, elapsed)
}

// finish closes the call even after cancellation, so it never stays In Progress.
func (p *Player) finish(id string, st *callState, s Script, elapsed float64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := "Done"
	update := st.update()
	update.Status = &done
	update.Duration = ptr(clock(elapsed))
	update.Categories = &s.Categories
	update.Summary = &s.Summary
	return p.Client.do(ctx, http.MethodPatch, "/calls/"+id, update, nil)
}

// callState accumulates sentiment as turns arrive.
type callState struct {
	caller, agent []float64
	history       []store.SentimentPoint
	alerts        int
}

func (st *callState) segment(t Turn) store.Segment {
	seg := store.Segment{
		Speaker:   t.Speaker,
		TimeStart: stamp(t.At),
		TimeEnd:   stamp(t.At + 2),
		Text:      t.Text,
		Emoji:     t.Emoji,
		PII:       t.PII,
	}
	if t.Score != nil {
		label := sentimentLabel(*t.Score)
		seg.Sentiment = &label
		seg.SentimentScore = t.Score
	}
	return seg
}

func (st *callState) apply(t Turn) store.CallUpdate {
	score := *t.Score
	switch t.Speaker {
	case speakerCustomer:
		st.caller = append(st.caller, score)
		if score <= alertScore {
			st.alerts++
		}
	case speakerAgent:
		st.agent = append(st.agent, score)
	}
	st.history = append(st.history, store.SentimentPoint{
		Time:   clock(t.At)[3:], // mm:ss
		Agent:  last(st.agent),
		Caller: last(st.caller),
	})
	u := st.update()
	u.Duration = ptr(clock(t.At))
	return u
}

func (st *callState) update() store.CallUpdate {
	callerAvg, agentAvg := mean(st.caller), mean(st.agent)
	history := append([]store.SentimentPoint(nil), st.history...)
	return store.CallUpdate{
		CallerSentiment:      ptr(titleLabel(callerAvg)),
		CallerSentimentTrend: ptr(trend(st.caller)),
		AgentSentimentTrend:  ptr(trend(st.agent)),
		CallerAvgSentiment:   &callerAvg,
		AgentAvgSentiment:    &agentAvg,
		AlertCount:           &st.alerts,
		SentimentHistory:     &history,
	}
}

func sentimentLabel(score float64) string {
	switch {
	case score >= positiveScore:
		return "positive"
	case score <= negativeScore:
		return "negative"
	default:
		return "neutral"
	}
}

func titleLabel(score float64) string {
	l := sentimentLabel(score)
	return string(l[0]-'a'+'A') + l[1:]
}

// trend compares the latest score with the average of the ones before it.
func trend(scores []float64) string {
	if len(scores) < 2 {
		return "Flat"
	}
	d := scores[len(scores)-1] - mean(scores[:len(scores)-1])
	switch {
	case d >= trendDelta:
		return "Up"
	case d <= -trendDelta:
		return "Down"
	default:
		return "Flat"
	}
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func last(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	return xs[len(xs)-1]
}

// stamp formats seconds as mm:ss.s, matching transcript timestamps.
func stamp(sec float64) string {
	m := int(sec) / 60
	return fmt.Sprintf("%02d:%04.1f", m, sec-float64(m*60))
}

// clock formats seconds as hh:mm:ss, matching call durations.
func clock(sec float64) string {
	s := int(sec)
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
}

// fakeNumber returns a number in the reserved 555-01xx fiction range.
func fakeNumber() string { return fmt.Sprintf("+1703555%04d", 100+rand.IntN(100)) }

func ptr[T any](v T) *T { return &v }
