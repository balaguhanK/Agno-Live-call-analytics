package analysis

import (
	"slices"
	"strings"
	"testing"

	"lca-backend/internal/store"
)

// The call from the LCA screenshot
func TestHighlights(t *testing.T) {
	tr := []store.Segment{
		{ID: 1, Speaker: SpeakerAgent, Text: "Calling Acme Bank. Uh, my name is Babu. How can I help you?"},
		{ID: 2, Speaker: SpeakerCustomer, Text: "Hi, I'm calling about my rewards card.", Insight: store.InsightIssue},
		{ID: 4, Speaker: SpeakerCustomer, Text: "I was wondering what's the cashback rate on this card?", Insight: store.InsightIssue},
		{ID: 5, Speaker: SpeakerAgent, Text: "Yes, I see here that the rewards card has a 2% cashback rate on your everyday spending. Is there anything else I can help you with?", Insight: store.InsightOutcome},
		{ID: 6, Speaker: SpeakerAgent, Text: "Okay, ma'am. I'll block your card right now.", Insight: store.InsightAction},
		{ID: 7, Speaker: SpeakerAssistant, Text: "Tip", Insight: store.InsightIssue},
	}
	want := []store.Highlight{
		{Line: 2, Kind: store.InsightIssue, Text: "I'm calling about my rewards card."}, // the greeting isn't the issue
		{Line: 4, Kind: store.InsightIssue, Text: "I was wondering what's the cashback rate on this card?"},
		{Line: 5, Kind: store.InsightOutcome, Text: "I see here that the rewards card has a 2% cashback rate on your everyday spending."},
		{Line: 6, Kind: store.InsightAction, Text: "I'll block your card right now."},
	}
	if got := Highlights(tr); !slices.Equal(got, want) {
		t.Errorf("Highlights =\n%+v\nwant\n%+v", got, want)
	}
}

// Every important sentence in a line, even on lines not flagged during the call
func TestHighlightsEverySentence(t *testing.T) {
	// As stored in your database: the filler line was labelled a promise during the call
	tr := slices.Clone(reservationCall)
	tr[1].Insight = store.InsightAction
	got := Highlights(tr)
	var marks []string
	for _, h := range got {
		marks = append(marks, h.Kind+": "+h.Text)
	}
	want := []string{
		"issue: I'm really not satisfied with your reservation fee.",
		"issue: I have lost my card, and I can't block it.",
		"issue: I want to block it.",
		"action: I'll block your call right now.",
		"action: I will provide you a link.",
	}
	if !slices.Equal(marks, want) {
		t.Errorf("highlights =\n%q\nwant\n%q", marks, want)
	}
	for _, h := range got {
		for _, s := range reservationCall {
			if s.ID == h.Line && !strings.Contains(s.Text, h.Text) {
				t.Errorf("highlight %q isn't in line %d %q", h.Text, h.Line, s.Text)
			}
		}
	}
}

func TestProductEntities(t *testing.T) {
	got := Entities([]store.Segment{
		{Speaker: SpeakerCustomer, Text: "I'm calling about my rewards card and my savings account."},
		{Speaker: SpeakerAgent, Text: "We offer universal life insurance. I called in order to check your internet plan."},
		{Speaker: SpeakerCustomer, Text: "Also cancel my order and my subscription."},
	})[store.EntityProduct]
	want := []string{"rewards card", "savings account", "universal life insurance", "internet plan", "order", "subscription"}
	if !slices.Equal(got, want) {
		t.Errorf("products = %q, want %q", got, want)
	}
}

func TestCategoryLines(t *testing.T) {
	tr := []store.Segment{
		{ID: 1, Speaker: SpeakerCustomer, Text: "I have lost my card."},
		{ID: 2, Speaker: SpeakerCustomer, Text: "And I was charged twice."},
	}
	got := CategoryLines(tr, []string{"Billing", "CardServices", "Sales"})
	if got["CardServices"] != 1 || got["Billing"] != 2 || len(got) != 2 {
		t.Errorf("CategoryLines = %v", got)
	}
}

func TestInsightPhrase(t *testing.T) {
	for _, tc := range []struct {
		seg  store.Segment
		want string
	}{
		{store.Segment{Speaker: SpeakerCustomer, Insight: store.InsightIssue, Text: "Hi, I'm calling about my rewards card."}, "I'm calling about my rewards card"},
		{store.Segment{Speaker: SpeakerAgent, Insight: store.InsightOutcome, Text: "Yes, I see here that the card has a 2% cashback rate. Anything else?"}, "I see here that the card has a 2% cashback rate"},
		{store.Segment{Speaker: SpeakerCustomer, Text: "Hello?"}, ""},
	} {
		if got := InsightPhrase(tc.seg); got != tc.want {
			t.Errorf("InsightPhrase(%q) = %q, want %q", tc.seg.Text, got, tc.want)
		}
	}
}
