package analysis

import (
	"testing"

	"lca-backend/internal/store"
)

func TestAnalyze(t *testing.T) {
	transcript := ScoreTranscript([]store.Segment{
		{Speaker: SpeakerAgent, TimeStart: "00:01.0", Text: "Thank you for calling, how can I help?"},
		{Speaker: SpeakerCustomer, TimeStart: "00:04.0", Text: "I was charged twice, this is terrible."},
		{Speaker: SpeakerAssistant, TimeStart: "00:04.0", Text: "Tip: offer a refund"},
		{Speaker: SpeakerAgent, TimeStart: "00:08.0", Text: "I have refunded the charge."},
		{Speaker: SpeakerCustomer, TimeStart: "00:12.0", Text: "Okay."},
		{Speaker: SpeakerCustomer, TimeStart: "00:15.0", Text: "Great, thanks so much for the help!"},
	})
	a := Analyze(transcript)
	st := a.Stats
	if st.CallerLines != 3 || st.AgentLines != 2 || st.CallerTalkPct+st.AgentTalkPct != 100 || st.CallerTalkPct < 50 {
		t.Errorf("stats = %+v", st)
	}
	if st.CallerStart != "Negative" || st.CallerEnd != "Positive" || st.CallerNegativeLines != 1 || st.AgentPositiveLines != 1 {
		t.Errorf("moods = %+v", st)
	}
	if q := a.Quotes.CallerLow; q == nil || q.Text != "I was charged twice, this is terrible." || q.Time != "00:04" {
		t.Errorf("caller low = %+v", q)
	}
	if q := a.Quotes.AgentBest; q == nil || q.Text != "Thank you for calling, how can I help?" {
		t.Errorf("agent best = %+v", q)
	}
	if a.Quotes.AgentWorst != nil {
		t.Errorf("agent never sounded negative, got %+v", a.Quotes.AgentWorst)
	}
	if a.Caller.Mood != "Negative → Positive" || a.Query.Issue != "I was charged twice, this is terrible." || a.Agent.Tone != "Positive" || a.Model != "" {
		t.Errorf("insights = %+v / %+v / %+v", a.Caller, a.Query, a.Agent)
	}

	empty := Analyze(nil)
	if empty.Caller.Mood != "Not captured" || empty.Agent.Tone != "Not captured" || empty.Stats.CallerTalkPct != 0 || empty.Quotes.CallerLow != nil {
		t.Errorf("empty analysis = %+v", empty)
	}
}

func TestMoodBackedUp(t *testing.T) {
	calm := store.CallStats{CallerLines: 4}
	upset := store.CallStats{CallerLines: 4, CallerNegativeLines: 1}
	for _, tc := range []struct {
		mood string
		st   store.CallStats
		want bool
	}{
		{"frustrated then relieved", calm, false},
		{"frustrated then relieved", upset, true},
		{"calm and polite", calm, true},
		{"satisfied", calm, true},
	} {
		if got := MoodBackedUp(tc.mood, tc.st); got != tc.want {
			t.Errorf("MoodBackedUp(%q, %d negative lines) = %v", tc.mood, tc.st.CallerNegativeLines, got)
		}
	}
}

func TestFirstMention(t *testing.T) {
	transcript := []store.Segment{
		{ID: 1, Speaker: SpeakerAgent, Text: "Is this about your bill?"},
		{ID: 2, Speaker: SpeakerCustomer, Text: "I'd like to cancel my subscription."},
		{ID: 3, Speaker: SpeakerCustomer, Text: "And I was charged twice."},
	}
	if s, ok := FirstMention(transcript, "Billing"); !ok || s.ID != 3 {
		t.Errorf("FirstMention(Billing) = %d %v, want line 3 (the agent's line doesn't count)", s.ID, ok)
	}
	if _, ok := FirstMention(transcript, "Sales"); ok {
		t.Error("Sales was never raised")
	}
}
