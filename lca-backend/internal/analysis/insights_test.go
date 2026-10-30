package analysis

import (
	"slices"
	"testing"

	"lca-backend/internal/store"
)

func TestDetectInsight(t *testing.T) {
	for _, tc := range []struct {
		speaker, text string
		issues        int
		want          string
	}{
		{SpeakerCustomer, "Hi, I'm calling about my rewards card.", 0, store.InsightIssue},
		{SpeakerCustomer, "My app isn’t working since the update", 0, store.InsightIssue},
		{SpeakerCustomer, "I was charged twice last month", 0, store.InsightIssue},
		{SpeakerCustomer, "I have refund issue", 0, store.InsightIssue},
		{SpeakerCustomer, "There's a problem with my internet bill", 0, store.InsightIssue},
		{SpeakerCustomer, "I got double charged for the same order", 0, store.InsightIssue},
		{SpeakerCustomer, "I still haven't received my new card", 0, store.InsightIssue},
		{SpeakerCustomer, "I'm locked out of my account", 0, store.InsightIssue},
		{SpeakerCustomer, "That sounds great. Thanks.", 0, ""},
		{SpeakerCustomer, "No problem, thank you so much", 0, ""},
		{SpeakerCustomer, "Okay, and the order number is 4521", 0, ""},
		{SpeakerCustomer, "I want to cancel", MaxIssues, ""}, // cap reached
		{SpeakerAgent, "I've refunded the duplicate charge.", 0, store.InsightOutcome},
		{SpeakerAgent, "I see here that the card has a 2% cashback rate.", 0, store.InsightOutcome},
		{SpeakerAgent, "I'll send you a confirmation email.", 0, store.InsightAction},
		{SpeakerAgent, "How can I help you today?", 0, ""},
		{SpeakerAssistant, "I'm calling about it", 0, ""},
	} {
		if got := DetectInsight(tc.speaker, tc.text, tc.issues); got != tc.want {
			t.Errorf("DetectInsight(%s, %q) = %q, want %q", tc.speaker, tc.text, got, tc.want)
		}
	}
}

func TestEntities(t *testing.T) {
	tr := []store.Segment{
		{Speaker: SpeakerAgent, Text: "Thank you for calling Acme Bank. My name is Babu. How can I help?"},
		{Speaker: SpeakerCustomer, Text: "Hi, this is Bob. My name is [NAME] on the account. What's the cashback, 2% or 3%?"},
		{Speaker: SpeakerAgent, Text: "It's 2 percent, and the refund of $45.20 arrives in 3 to 5 business days, by Friday. See acmebank.com."},
		{Speaker: SpeakerAssistant, Text: "My name is Ignored at Other Bank"},
		{Speaker: SpeakerCustomer, Text: "Thanks, Great. See you on March 3rd."},
	}
	got := Entities(tr)
	want := map[string][]string{
		store.EntityPerson:       {"Babu (Agent)", "Bob (Customer)"}, // who said it decides whose name it is
		store.EntityOrganization: {"Acme Bank", "acmebank.com"},
		store.EntityQuantity:     {"2%", "3%", "2 percent", "$45.20", "3 to 5 business days"},
		store.EntityDate:         {"Friday", "March 3rd"},
	}
	for kind, w := range want {
		if !slices.Equal(got[kind], w) {
			t.Errorf("%s = %q, want %q", kind, got[kind], w)
		}
	}
}

func TestReferenceEntities(t *testing.T) {
	tr := []store.Segment{
		{Speaker: SpeakerCustomer, Text: "Already QX123."},
		{Speaker: SpeakerCustomer, Text: "My order number is 4521 and the ticket #a-77b."},
		{Speaker: SpeakerAgent, Text: "Call back after 5pm, it's the 2nd plan with 10GB, ref qx123."},
		{Speaker: SpeakerCustomer, Text: "Card [CARD] and order of 3 items"},
	}
	got := Entities(tr)[store.EntityReference]
	if want := []string{"QX123", "4521", "A-77B"}; !slices.Equal(got, want) {
		t.Errorf("references = %q, want %q", got, want)
	}
}

func TestPeople(t *testing.T) {
	tr := []store.Segment{
		{Speaker: SpeakerAgent, Text: "Thank you for calling Acme Bank, this is Bob. How can I help?"},
		{Speaker: SpeakerCustomer, Text: "Hi Bob. My name is Ravi Kumar and I am sorry to bother you."},
		{Speaker: SpeakerAgent, Text: "No problem. Thank you, Ravi. Is it Friday you were charged?"},
		{Speaker: SpeakerAssistant, Text: "My name is Ignored"},
	}
	if got, want := People(tr, "Bob Smith"), []string{"Bob Smith (Agent)", "Ravi Kumar (Customer)"}; !slices.Equal(got, want) {
		t.Errorf("People = %q, want %q", got, want)
	}
	// "Thank you, Ravi" is the same customer as "Ravi Kumar", so not listed twice
	// Without the account, the name the agent gave
	if got, want := People(tr, ""), []string{"Bob (Agent)", "Ravi Kumar (Customer)"}; !slices.Equal(got, want) {
		t.Errorf("People without account = %q, want %q", got, want)
	}
	// Nobody named: only the agent's account, never a guess
	if got := People([]store.Segment{{Speaker: SpeakerCustomer, Text: "I am locked out. This is the third time."}}, "Bob Smith"); !slices.Equal(got, []string{"Bob Smith (Agent)"}) {
		t.Errorf("People with no names said = %q", got)
	}
}

// What callers say about order numbers, amounts and dates on a phone call
func TestEntitiesCallersSay(t *testing.T) {
	tr := []store.Segment{
		{Speaker: SpeakerCustomer, Text: "My order number is four five two one and I paid 500 rupees."},
		{Speaker: SpeakerCustomer, Text: "The order ID is A B 1 2 3. I was charged ₹1,250 on the 3rd."},
		{Speaker: SpeakerAgent, Text: "I see Rs 250 from 15th of March 2025. That's 10. I'll call you at 5 pm, or 10:30 next Monday."},
		{Speaker: SpeakerCustomer, Text: "I want to block my card. 18 point. I bought 3 items, ref ZX-4471."},
	}
	got := Entities(tr)
	for kind, want := range map[string][]string{
		store.EntityReference: {"4521", "AB123", "ZX-4471"},
		store.EntityQuantity:  {"500 rupees", "₹1,250", "Rs 250", "10", "18", "3 items"},
		store.EntityDate:      {"on the 3rd", "15th of March 2025", "5 pm", "10:30", "next Monday"},
	} {
		if !slices.Equal(got[kind], want) {
			t.Errorf("%s = %q, want %q", kind, got[kind], want)
		}
	}
}

func TestRuleActions(t *testing.T) {
	tr := []store.Segment{
		{Speaker: SpeakerCustomer, Text: "I'll wait, I've got time."},
		{Speaker: SpeakerAgent, TimeStart: "00:10.0", Text: "I've refunded the charge."},
		{Speaker: SpeakerAgent, Text: "How are you?"},
	}
	got := RuleActions(tr)
	if len(got) != 1 || got[0].Title != "I've refunded the charge." || got[0].Owner != "Agent" || got[0].Detail != "Said by the agent at 00:10." {
		t.Errorf("RuleActions = %+v", got)
	}
	if got := RuleActions(nil); got == nil || len(got) != 0 {
		t.Errorf("no actions = %#v", got)
	}
}

func TestRuleInsights(t *testing.T) {
	pos, neg := 2.0, -2.0
	tr := []store.Segment{
		{Speaker: SpeakerCustomer, Text: "I'm calling about a double charge", Insight: store.InsightIssue, SentimentScore: &neg},
		{Speaker: SpeakerAgent, Text: "I've refunded it.", Insight: store.InsightOutcome, SentimentScore: &pos},
		{Speaker: SpeakerAgent, Text: "I'll email you the receipt.", Insight: store.InsightAction, SentimentScore: &pos},
	}
	g := RuleInsights(tr, []string{"Billing"})
	if g.Topic != "I'm calling about a double charge" || g.Politeness != "yes" ||
		g.Actions != "I've refunded it. I'll email you the receipt." || g.Summary == "" || g.Model != "" {
		t.Errorf("RuleInsights = %+v", g)
	}
	if g := RuleInsights(nil, []string{"ManagerEscalation"}); g.Topic != "Manager escalation" || g.Actions != "No agent actions detected." {
		t.Errorf("empty call insights = %+v", g)
	}
}
