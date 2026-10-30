package analysis

import (
	"slices"
	"testing"

	"lca-backend/internal/store"
)

// The call the PCA page showed an invented action for
var orderCall = []store.Segment{
	{ID: 1, Speaker: SpeakerCustomer, TimeStart: "00:00.0", Text: "Hello."},
	{ID: 2, Speaker: SpeakerAgent, TimeStart: "00:03.0", Text: "So can you help me?"},
	{ID: 3, Speaker: SpeakerCustomer, TimeStart: "00:05.0", Text: "I want to cancel an order."},
	{ID: 4, Speaker: SpeakerCustomer, TimeStart: "00:24.0", Text: "Already QX123."},
	{ID: 5, Speaker: SpeakerAgent, TimeStart: "00:31.0", Text: "You you want refund or exchange?"},
	{ID: 6, Speaker: SpeakerCustomer, TimeStart: "00:39.0", Text: "I want you to change my order."},
	{ID: 7, Speaker: SpeakerAssistant, TimeStart: "00:39.0", Text: "Refunds take 3 to 5 business days."},
}

func TestFindEvidence(t *testing.T) {
	for _, tc := range []struct {
		quote    string
		speakers []string
		wantID   int // 0: no evidence
	}{
		{"You want refund or exchange?", []string{SpeakerAgent}, 5},                                            // a dropped filler word
		{"you you want refund or exchange", nil, 5},                                                            // word for word
		{"I want to cancel an order", []string{SpeakerCustomer}, 3},                                            // exact
		{"Already QX123", []string{SpeakerCustomer}, 4},                                                        // short but exact
		{"Will you please tell me the order number so I can look into the details", []string{SpeakerAgent}, 0}, // made up
		{"I want to cancel an order", []string{SpeakerAgent}, 0},                                               // right words, wrong speaker
		{"Refunds take 3 to 5 business days", nil, 0},                                                          // the assistant isn't the call... unless asked
		{"order", []string{SpeakerCustomer}, 0},                                                                // too short to point at a line
		{"", nil, 0},
	} {
		speakers := tc.speakers
		if speakers == nil {
			speakers = []string{SpeakerAgent, SpeakerCustomer}
		}
		seg, ok := FindEvidence(orderCall, tc.quote, speakers...)
		if got := map[bool]int{true: seg.ID, false: 0}[ok]; got != tc.wantID {
			t.Errorf("FindEvidence(%q) = line %d, want %d", tc.quote, got, tc.wantID)
		}
	}
}

func TestUnsupportedFacts(t *testing.T) {
	call := append(slices.Clone(orderCall),
		store.Segment{Speaker: SpeakerAgent, Text: "The refund of $45.20 reaches you in three to five days, on the 3rd."})
	for text, want := range map[string][]string{
		"The customer wanted to cancel order QX123.":        nil,
		"A refund of $45.20 in 3-5 days.":                   nil, // digits for the number words said
		"The agent refunded 50 dollars within 7 days.":      {"50", "7"},
		"The order QX124 was cancelled.":                    {"qx124"},
		"The customer asked about the order.":               nil,
		"Refunds take 3 to 5 business days, as of the 3rd.": nil,
	} {
		if got := UnsupportedFacts(text, call); !slices.Equal(got, want) {
			t.Errorf("UnsupportedFacts(%q) = %q, want %q", text, got, want)
		}
	}
	// What only the assistant said wasn't said on the call
	if Grounded("Refunds take 3 to 5 business days.", orderCall) {
		t.Error("assistant-only numbers counted as said on the call")
	}
}
