package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The same behaviour is required of the in-memory store and Postgres. The
// Postgres run needs TEST_DATABASE_URL and works in a throwaway schema, so
// it never touches the app's real tables.

type storeUnderTest interface {
	CountUsers(context.Context) (int, error)
	GetUser(context.Context, string) (User, error)
	ListUsers(context.Context) ([]User, error)
	CreateUser(context.Context, User) (User, error)
	SetUserActive(context.Context, string, bool) error
	SetPasswordHash(context.Context, string, string) error
	TouchSignIn(context.Context, string) error
	DeleteUser(context.Context, string) error
	ListCalls(context.Context, CallQuery) ([]Call, int, error)
	CallCountsByAgent(context.Context) (map[string]int, error)
	GetCall(context.Context, string) (Call, error)
	CreateCall(context.Context, NewCall) (Call, error)
	UpdateCall(context.Context, string, CallUpdate) (Call, error)
	AppendSegment(context.Context, string, Segment) (Segment, string, error)
	UpdateSegment(context.Context, string, Segment) error
	SetSegmentSentiment(context.Context, string, int, string, float64) error
}

func TestConformanceMemory(t *testing.T) { runConformance(t, NewMemory()) }

func TestConformancePostgres(t *testing.T) {
	db := openTestPostgres(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runConformance(t, db)
}

// openTestPostgres connects to TEST_DATABASE_URL inside a fresh schema that
// is dropped when the test ends.
func openTestPostgres(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres tests")
	}
	ctx := context.Background()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "lca_test_" + hex.EncodeToString(b)

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	// public stays on the path so extension types (pgvector) resolve
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func runConformance(t *testing.T, s storeUnderTest) {
	ctx := context.Background()
	str := func(v string) *string { return &v }

	// ── Users ──
	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("fresh store has %d users", n)
	}
	u, err := s.CreateUser(ctx, User{Email: "bob@x.com", Name: "Bob", Role: "agent", AgentName: str("BobS"), PasswordHash: "h1"})
	if err != nil || !u.Active || u.CreatedAt == "" || u.LastSignIn != nil {
		t.Fatalf("CreateUser = %+v, %v", u, err)
	}
	if _, err := s.CreateUser(ctx, User{Email: "bob@x.com", Name: "B", Role: "agent", PasswordHash: "h"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate email err = %v", err)
	}
	if _, err := s.CreateUser(ctx, User{Email: "b2@x.com", Name: "B", Role: "agent", AgentName: str("BobS"), PasswordHash: "h"}); !errors.Is(err, ErrAgentNameTaken) {
		t.Errorf("duplicate agent err = %v", err)
	}
	if _, err := s.CreateUser(ctx, User{Email: "admin@x.com", Name: "Admin", Role: "admin", PasswordHash: "h2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserActive(ctx, "bob@x.com", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPasswordHash(ctx, "bob@x.com", "h9"); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchSignIn(ctx, "bob@x.com"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUser(ctx, "bob@x.com")
	if err != nil || got.Active || got.PasswordHash != "h9" || got.LastSignIn == nil || *got.AgentName != "BobS" {
		t.Errorf("GetUser = %+v, %v", got, err)
	}
	if _, err := s.GetUser(ctx, "nobody@x.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user err = %v", err)
	}
	for _, e := range []error{s.SetUserActive(ctx, "nobody@x.com", true), s.DeleteUser(ctx, "nobody@x.com")} {
		if !errors.Is(e, ErrNotFound) {
			t.Errorf("update of missing user err = %v", e)
		}
	}
	users, _ := s.ListUsers(ctx)
	if len(users) != 2 || users[0].Email != "bob@x.com" {
		t.Errorf("ListUsers = %+v", users)
	}

	// ── Calls ──
	at := func(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format(time.RFC3339) }
	for _, n := range []NewCall{
		{ID: "c1", Agent: "BobS", CallerPhoneNumber: "+15550001", InitiationTimestamp: at(30 * time.Minute)},
		{ID: "c2", Agent: "BabuS", CallerPhoneNumber: "+15550002", InitiationTimestamp: at(3 * time.Hour)},
		{ID: "c3", Agent: "BobS", CallerPhoneNumber: "+15550003", InitiationTimestamp: at(10 * time.Minute)},
	} {
		c, err := s.CreateCall(ctx, n)
		if err != nil || c.Status != "In Progress" || c.CallerSentiment != "Neutral" || c.Duration != "00:00:00" || len(c.Categories) != 0 {
			t.Fatalf("CreateCall(%s) = %+v, %v", n.ID, c, err)
		}
	}
	if _, err := s.CreateCall(ctx, NewCall{ID: "c1"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate call err = %v", err)
	}
	cats := []string{"Billing"}
	hist := []SentimentPoint{{Time: "00:05", Agent: 1, Caller: -2}}
	upd, err := s.UpdateCall(ctx, "c1", CallUpdate{Status: str("Done"), Categories: &cats, Summary: str("All good"), SentimentHistory: &hist})
	if err != nil || upd.Status != "Done" || !slices.Equal(upd.Categories, cats) || upd.Summary == nil || upd.Summary.Text != "All good" ||
		len(upd.SentimentHistory) != 1 || upd.SentimentHistory[0].Caller != -2 || upd.Agent != "BobS" {
		t.Errorf("UpdateCall = %+v, %v", upd, err)
	}
	analysis := CallAnalysis{
		Stats:  CallStats{CallerLines: 2, CallerTalkPct: 40, CallerStart: "Negative", CallerEnd: "Positive"},
		Query:  QueryInsight{Issue: "Double charge", Outcome: "Resolved"},
		Agent:  AgentInsight{Tone: "Calm", Strengths: []string{"Apologised"}, Rating: 4},
		Quotes: AnalysisQuotes{CallerLow: &Quote{Time: "00:05", Text: "This is terrible", Score: -3}},
		Model:  "gemma2:2b",
	}
	upd, err = s.UpdateCall(ctx, "c1", CallUpdate{Analysis: &analysis})
	if err != nil || upd.Analysis == nil || upd.Analysis.Query.Outcome != "Resolved" || upd.Analysis.Agent.Strengths[0] != "Apologised" ||
		upd.Analysis.Quotes.CallerLow.Text != "This is terrible" || upd.Summary == nil {
		t.Errorf("UpdateCall analysis = %+v, %v", upd.Analysis, err)
	}
	analysis.Agent.Strengths[0] = "changed by caller"
	if got, _ := s.GetCall(ctx, "c1"); got.Analysis == nil || got.Analysis.Agent.Strengths[0] != "Apologised" || got.Analysis.Stats.CallerTalkPct != 40 {
		t.Errorf("stored analysis = %+v", got.Analysis)
	}
	if _, err := s.UpdateCall(ctx, "nope", CallUpdate{Status: str("Done")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing call err = %v", err)
	}

	ids := func(cs []Call) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.ID
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		q     CallQuery
		want  []string
		total int
	}{
		{"newest first", CallQuery{Desc: true}, []string{"c3", "c1", "c2"}, 3},
		{"agent isolation", CallQuery{Agent: "BabuS"}, []string{"c2"}, 1},
		{"time window", CallQuery{Since: time.Now().Add(-2 * time.Hour), Desc: true}, []string{"c3", "c1"}, 2},
		{"search phone", CallQuery{Search: "0003"}, []string{"c3"}, 1},
		{"search category, any case", CallQuery{Search: "billing"}, []string{"c1"}, 1},
		{"search status", CallQuery{Search: "done"}, []string{"c1"}, 1},
		{"search escapes % literally", CallQuery{Search: "%"}, []string{}, 0},
		{"sort by id", CallQuery{Sort: "id"}, []string{"c1", "c2", "c3"}, 3},
		{"page 2", CallQuery{Sort: "id", Limit: 2, Offset: 2}, []string{"c3"}, 3},
		{"past the end", CallQuery{Sort: "id", Limit: 2, Offset: 9}, []string{}, 3},
		{"only ids", CallQuery{IDs: []string{"c2", "c3"}, Sort: "id"}, []string{"c2", "c3"}, 2},
	} {
		calls, total, err := s.ListCalls(ctx, tc.q)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := ids(calls); total != tc.total || !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v (total %d), want %v (total %d)", tc.name, got, total, tc.want, tc.total)
		}
	}
	counts, _ := s.CallCountsByAgent(ctx)
	if counts["BobS"] != 2 || counts["BabuS"] != 1 {
		t.Errorf("counts = %v", counts)
	}

	// ── Transcript ──
	for i, text := range []string{"hello", "world"} {
		seg, agent, err := s.AppendSegment(ctx, "c3", Segment{Speaker: "CUSTOMER", Text: text, TimeStart: "00:01.0", TimeEnd: "00:02.0", PII: []string{"CARD"}})
		if err != nil || seg.ID != i+1 || agent != "BobS" {
			t.Fatalf("AppendSegment = %+v %q %v", seg, agent, err)
		}
	}
	if _, _, err := s.AppendSegment(ctx, "nope", Segment{Speaker: "AGENT", Text: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("append to missing call err = %v", err)
	}
	if err := s.SetSegmentSentiment(ctx, "c3", 2, "positive", 2.5); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSegmentSentiment(ctx, "c3", 9, "positive", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("sentiment on missing segment err = %v", err)
	}
	full, err := s.GetCall(ctx, "c3")
	if err != nil || len(full.Transcript) != 2 {
		t.Fatalf("GetCall = %+v, %v", full, err)
	}
	if s0, s1 := full.Transcript[0], full.Transcript[1]; s0.Text != "hello" || s0.Sentiment != nil ||
		s1.Sentiment == nil || *s1.Sentiment != "positive" || *s1.SentimentScore != 2.5 || !slices.Equal(s1.PII, []string{"CARD"}) {
		t.Errorf("transcript = %+v / %+v", s0, s1)
	}
	// Insight tags and inline assistant answers are stored with the line
	tagged, _, err := s.AppendSegment(ctx, "c3", Segment{Speaker: "CUSTOMER", Text: "I'm calling about my card",
		TimeStart: "00:03.0", TimeEnd: "00:04.0", Insight: InsightIssue})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendSegment(ctx, "c3", Segment{Speaker: "AGENT_ASSISTANT", Text: "Offer the card guide",
		TimeStart: "00:03.0", TimeEnd: "00:04.0", Assist: &SegmentAssist{Query: "my card", Model: "m",
			Sources: []AssistSource{{Title: "Cards", Section: "Rewards", File: "cards.md", Excerpt: "2% back"}}}}); err != nil {
		t.Fatal(err)
	}
	withMeta, _ := s.GetCall(ctx, "c3")
	if n := len(withMeta.Transcript); n != 4 || withMeta.Transcript[tagged.ID-1].Insight != InsightIssue {
		t.Fatalf("tagged transcript = %+v", withMeta.Transcript)
	}
	if a := withMeta.Transcript[3].Assist; a == nil || a.Query != "my card" || len(a.Sources) != 1 || a.Sources[0].Excerpt != "2% back" ||
		withMeta.Transcript[3].Insight != "" || withMeta.Transcript[0].Assist != nil {
		t.Errorf("assistant line = %+v", withMeta.Transcript[3])
	}

	// A suggestion is updated in place once the model has written it
	edited := withMeta.Transcript[3]
	edited.Text = "Say sorry, then block the card"
	edited.Assist.Say, edited.Assist.Steps, edited.Assist.Pending = "I'll block it now.", []string{"Block the card"}, false
	if err := s.UpdateSegment(ctx, "c3", edited); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSegment(ctx, "c3", Segment{ID: 99, Text: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing segment err = %v", err)
	}
	after, _ := s.GetCall(ctx, "c3")
	if got := after.Transcript[3]; got.Text != edited.Text || got.Assist == nil || got.Assist.Say != "I'll block it now." ||
		len(got.Assist.Steps) != 1 || len(got.Assist.Sources) != 1 || got.Speaker != "AGENT_ASSISTANT" {
		t.Errorf("updated line = %+v / %+v", got, got.Assist)
	}

	if _, err := s.GetCall(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing call err = %v", err)
	}
	if listed, _, _ := s.ListCalls(ctx, CallQuery{IDs: []string{"c3"}}); len(listed) != 1 || listed[0].Transcript != nil {
		t.Errorf("list must not include transcripts: %+v", listed)
	}

	if err := s.DeleteUser(ctx, "admin@x.com"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountUsers(ctx); n != 1 {
		t.Errorf("users after delete = %d", n)
	}
}
