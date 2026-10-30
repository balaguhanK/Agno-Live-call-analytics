package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemorySeedsUsersOnly(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	hash := func(p string) (string, error) { return "h:" + p, nil }

	if err := m.SeedUsers(ctx, hash); err != nil {
		t.Fatal(err)
	}
	u, err := m.GetUser(ctx, "admin@cxplatform.com")
	if err != nil || u.PasswordHash != "h:admin123" || !u.Active {
		t.Fatalf("GetUser = %+v, %v", u, err)
	}
	if calls, total, _ := m.ListCalls(ctx, CallQuery{}); total != 0 || len(calls) != 0 {
		t.Fatalf("expected no demo calls, got %d", total)
	}
}

// addCall creates a call that started ago before now.
func addCall(t *testing.T, m *Memory, id, agent, phone string, ago time.Duration, cats ...string) {
	t.Helper()
	ctx := context.Background()
	_, err := m.CreateCall(ctx, NewCall{ID: id, Agent: agent, CallerPhoneNumber: phone,
		InitiationTimestamp: time.Now().Add(-ago).UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) > 0 {
		if _, err := m.UpdateCall(ctx, id, CallUpdate{Categories: &cats}); err != nil {
			t.Fatal(err)
		}
	}
}

func ids(calls []Call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.ID
	}
	return out
}

func TestMemoryListCallsQuery(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	addCall(t, m, "c1", "BobS", "+15550001", 30*time.Minute, "Billing")
	addCall(t, m, "c2", "BabuS", "+15550002", 3*time.Hour)
	addCall(t, m, "c3", "BobS", "+15550003", 10*time.Minute)
	addCall(t, m, "c4", "BobS", "+15550004", 50*time.Hour)

	cases := []struct {
		name  string
		q     CallQuery
		want  []string
		total int
	}{
		{"newest first by default", CallQuery{Desc: true}, []string{"c3", "c1", "c2", "c4"}, 4},
		{"agent isolation", CallQuery{Agent: "BabuS"}, []string{"c2"}, 1},
		{"time window", CallQuery{Since: time.Now().Add(-2 * time.Hour), Desc: true}, []string{"c3", "c1"}, 2},
		{"search phone", CallQuery{Search: "0004"}, []string{"c4"}, 1},
		{"search category, any case", CallQuery{Search: "billing"}, []string{"c1"}, 1},
		{"sort by id ascending", CallQuery{Sort: "id"}, []string{"c1", "c2", "c3", "c4"}, 4},
		{"page 2", CallQuery{Sort: "id", Limit: 3, Offset: 3}, []string{"c4"}, 4},
		{"past the end", CallQuery{Limit: 3, Offset: 9}, []string{}, 4},
		{"only ids", CallQuery{IDs: []string{"c2", "c4"}, Sort: "id"}, []string{"c2", "c4"}, 2},
	}
	for _, c := range cases {
		calls, total, err := m.ListCalls(ctx, c.q)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := ids(calls)
		if total != c.total || len(got) != len(c.want) {
			t.Errorf("%s: got %v (total %d), want %v (total %d)", c.name, got, total, c.want, c.total)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}

	counts, _ := m.CallCountsByAgent(ctx)
	if counts["BobS"] != 3 || counts["BabuS"] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

func TestMemoryTranscript(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	addCall(t, m, "c1", "BobS", "+15550001", time.Minute)
	seg, agent, err := m.AppendSegment(ctx, "c1", Segment{Speaker: "CUSTOMER", Text: "hi"})
	if err != nil || agent != "BobS" || seg.ID != 1 {
		t.Fatalf("AppendSegment = %+v, %q, %v", seg, agent, err)
	}
	full, _ := m.GetCall(ctx, "c1")
	if len(full.Transcript) != 1 || full.Transcript[0].Text != "hi" {
		t.Fatalf("transcript = %+v", full.Transcript)
	}
}

func TestMemoryUserConflicts(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	name := "A1"
	if _, err := m.CreateUser(ctx, User{Email: "a@x.com", AgentName: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateUser(ctx, User{Email: "a@x.com"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate email err = %v", err)
	}
	if _, err := m.CreateUser(ctx, User{Email: "b@x.com", AgentName: &name}); !errors.Is(err, ErrAgentNameTaken) {
		t.Fatalf("duplicate agent err = %v", err)
	}
	if err := m.DeleteUser(ctx, "missing@x.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing err = %v", err)
	}
}

func TestMemorySetSegmentSentiment(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	addCall(t, m, "c1", "BobS", "+15550001", time.Minute)
	if _, _, err := m.AppendSegment(ctx, "c1", Segment{Speaker: "CUSTOMER", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetSegmentSentiment(ctx, "c1", 1, "positive", 2); err != nil {
		t.Fatal(err)
	}
	full, _ := m.GetCall(ctx, "c1")
	if s := full.Transcript[0]; s.Sentiment == nil || *s.Sentiment != "positive" || *s.SentimentScore != 2 {
		t.Errorf("segment = %+v", s)
	}
	if err := m.SetSegmentSentiment(ctx, "c1", 9, "positive", 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing segment err = %v", err)
	}
}
