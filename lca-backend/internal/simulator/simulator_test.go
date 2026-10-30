package simulator

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"lca-backend/internal/api"
	"lca-backend/internal/events"
	"lca-backend/internal/session"
	"lca-backend/internal/store"
)

const testToken = "test-ingest-token-0123456789"

// newBackend runs the real API over an in-memory store.
func newBackend(t *testing.T) (*store.Memory, *Client) {
	t.Helper()
	mem := store.NewMemory()
	srv := httptest.NewServer(api.New(api.Options{
		Store:       mem,
		Sessions:    session.NewStore(nil),
		Hub:         events.NewHub(nil),
		IngestToken: testToken,
		Health:      func(context.Context) error { return nil },
	}))
	t.Cleanup(srv.Close)
	return mem, &Client{BaseURL: srv.URL, Token: testToken, HTTP: srv.Client()}
}

func instant(context.Context, time.Duration) error { return nil }

func TestScriptsAreValid(t *testing.T) {
	scripts, err := Scripts()
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) == 0 {
		t.Fatal("no scripts")
	}
	for _, s := range scripts {
		if s.Summary == "" || len(s.Categories) == 0 || len(s.Turns) == 0 {
			t.Errorf("%s: missing summary, categories or turns", s.Name)
		}
		prev := 0.0
		for i, turn := range s.Turns {
			if turn.At < prev {
				t.Errorf("%s turn %d: time goes backwards", s.Name, i)
			}
			prev = turn.At
			if (turn.Score == nil) != (turn.Speaker == "AGENT_ASSISTANT") {
				t.Errorf("%s turn %d: only assistant turns may omit a score", s.Name, i)
			}
		}
	}
}

func TestPlayStreamsWholeCall(t *testing.T) {
	mem, client := newBackend(t)
	scripts, _ := Scripts()
	s := scripts[0]

	p := &Player{Client: client, Sleep: instant}
	id, err := p.Play(context.Background(), "BobS", s)
	if err != nil {
		t.Fatal(err)
	}

	call, err := mem.GetCall(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if call.Agent != "BobS" || call.Status != "Done" {
		t.Fatalf("agent=%q status=%q", call.Agent, call.Status)
	}
	if len(call.Transcript) != len(s.Turns) {
		t.Fatalf("transcript has %d lines, want %d", len(call.Transcript), len(s.Turns))
	}
	if call.Summary == nil || call.Summary.Text != s.Summary {
		t.Fatalf("summary = %v", call.Summary)
	}
	if call.AlertCount != 1 {
		t.Fatalf("alertCount = %d, want 1 for the frustrated caller line", call.AlertCount)
	}
	if want := clock(s.Turns[len(s.Turns)-1].At); call.Duration != want {
		t.Fatalf("duration = %q, want %q", call.Duration, want)
	}
}

func TestPlayClosesCallWhenCancelled(t *testing.T) {
	mem, client := newBackend(t)
	scripts, _ := Scripts()

	ctx, cancel := context.WithCancel(context.Background())
	turns := 0
	p := &Player{Client: client, Sleep: func(ctx context.Context, _ time.Duration) error {
		if turns++; turns > 2 {
			cancel()
		}
		return ctx.Err()
	}}
	id, err := p.Play(ctx, "BabuS", scripts[1])
	if err != nil {
		t.Fatal(err)
	}
	call, _ := mem.GetCall(context.Background(), id)
	if call.Status != "Done" || len(call.Transcript) != 2 {
		t.Fatalf("status=%q lines=%d, want Done with 2 lines", call.Status, len(call.Transcript))
	}
}

func TestBadTokenIsRejected(t *testing.T) {
	_, client := newBackend(t)
	client.Token = "wrong"
	scripts, _ := Scripts()
	if _, err := (&Player{Client: client, Sleep: instant}).Play(context.Background(), "BobS", scripts[0]); err == nil {
		t.Fatal("expected 401 error")
	}
}

func TestSentimentHelpers(t *testing.T) {
	cases := []struct {
		scores []float64
		want   string
	}{
		{nil, "Flat"},
		{[]float64{0, 2}, "Up"},
		{[]float64{1, -1}, "Down"},
		{[]float64{1, 1.2}, "Flat"},
	}
	for _, c := range cases {
		if got := trend(c.scores); got != c.want {
			t.Errorf("trend(%v) = %q, want %q", c.scores, got, c.want)
		}
	}
	if titleLabel(-2) != "Negative" || titleLabel(0) != "Neutral" || titleLabel(1.5) != "Positive" {
		t.Error("titleLabel mismatch")
	}
	if stamp(65.5) != "01:05.5" || clock(3725) != "01:02:05" {
		t.Errorf("stamp=%q clock=%q", stamp(65.5), clock(3725))
	}
}
