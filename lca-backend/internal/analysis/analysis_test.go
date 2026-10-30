package analysis

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"lca-backend/internal/store"
)

func TestScore(t *testing.T) {
	cases := []struct {
		text string
		want string // label
	}{
		{"Thank you so much, that's great!", "positive"},
		{"This is unacceptable, I'm really frustrated.", "negative"},
		{"I'd like to check my account.", "neutral"},
		{"That is not good at all.", "negative"},
		// Naming the reason for the call is a neutral request, not a complaint
		{"It's not a problem.", "neutral"},
		{"I'd like to cancel my subscription please", "neutral"},
		{"I was charged twice for the same item", "neutral"},
		{"My account is locked, can you help me unlock it?", "neutral"},
		{"I'm waiting for my delivery, can you check the status?", "neutral"},
		{"I want to report a problem with my login", "neutral"},
		{"I got an error when I tried to pay", "neutral"},
		{"I'm sorry, could you repeat that?", "neutral"},
		// The agent asking about the problem isn't negative either
		{"Can I know what is your problem?", "neutral"},
		{"What seems to be the problem?", "neutral"},
		{"What's wrong with your card?", "neutral"},
		{"Sorry, what was the error?", "neutral"},
		{"No thanks, that's all.", "positive"},
		// Frustration said without the old word list's words
		{"This is the third time I'm calling about this", "negative"},
		{"I've been on hold for an hour", "negative"},
		{"It's still not working", "negative"},
		{"My card isn’t working, I’m not happy", "negative"}, // curly apostrophes
		{"My card isnt working and im not happy", "negative"},
		{"I'm really unhappy with this service", "negative"},
		{"Nobody has helped me, I'm fed up", "negative"},
		{"I'm not satisfied with the answer", "negative"},
		{"Your service is pathetic and the staff are rude", "negative"},
		{"Thanks, but this is ridiculous", "negative"},
	}
	for _, c := range cases {
		if got := Label(Score(c.text)); got != c.want {
			t.Errorf("Score(%q) = %.2f (%s), want %s", c.text, Score(c.text), got, c.want)
		}
	}
	if s := Score(strings.Repeat("terrible ", 20)); s != -maxScore {
		t.Errorf("score not clamped: %v", s)
	}
}

func TestRedact(t *testing.T) {
	cases := []struct {
		in, want string
		kinds    []string
	}{
		{"my card is 4111 1111 1111 1111 thanks", "my card is [CARD] thanks", []string{"CARD"}},
		{"email me at jane.doe@example.com", "email me at [EMAIL]", []string{"EMAIL"}},
		{"call me on +1 (703) 555-0144", "call me on [PHONE]", []string{"PHONE"}},
		{"my ssn is 123-45-6789", "my ssn is [SSN]", []string{"SSN"}},
		{"order 12345 arrives in 3 days", "order 12345 arrives in 3 days", []string{}},
		// Fails the Luhn check, so it isn't treated as a card (but is long enough to be a number)
		{"ref 1234 5678 9012 3456", "ref [PHONE]", []string{"PHONE"}},
	}
	for _, c := range cases {
		got, kinds := Redact(c.in)
		if got != c.want || !slices.Equal(kinds, c.kinds) {
			t.Errorf("Redact(%q) = %q %v, want %q %v", c.in, got, kinds, c.want, c.kinds)
		}
	}
}

func seg(speaker, text string) store.Segment {
	s := Score(text)
	return store.Segment{Speaker: speaker, Text: text, TimeStart: "00:01.0", SentimentScore: &s}
}

func TestCategoriesAndSuggestions(t *testing.T) {
	transcript := []store.Segment{
		seg(SpeakerAgent, "How can I help?"),
		seg(SpeakerCustomer, "I was charged twice on my bill."),
		seg(SpeakerCustomer, "This is unacceptable, let me speak to a manager!"),
	}
	cats := Categories(transcript)
	// Main topic first: the caller raised billing before asking for a manager;
	// the agent's "How can I help?" matches AgentGreeting, listed last
	if !slices.Equal(cats, []string{"Billing", "ManagerEscalation", "AgentGreeting"}) {
		t.Errorf("categories = %v", cats)
	}
	if got := Categories(transcript[:1]); !slices.Equal(got, []string{"AgentGreeting"}) {
		t.Errorf("default category = %v", got)
	}

	tips := Suggestions(transcript[2], transcript[:2])
	if len(tips) != 2 || tips[0] != frustrationSuggestion {
		t.Fatalf("tips = %v", tips)
	}
	// The same tip isn't repeated later in the call
	earlier := append(slices.Clone(transcript), store.Segment{Speaker: SpeakerAssistant, Text: tips[1]})
	again := Suggestions(seg(SpeakerCustomer, "I want a manager now."), earlier)
	if len(again) != 0 {
		t.Errorf("repeated tips: %v", again)
	}
	if Suggestions(seg(SpeakerAgent, "Let me get a manager."), nil) != nil {
		t.Error("agent lines should not trigger tips")
	}
}

func TestAggregate(t *testing.T) {
	u := Aggregate([]store.Segment{
		seg(SpeakerCustomer, "This is terrible and unacceptable."),
		seg(SpeakerAssistant, "tip"),
		seg(SpeakerAgent, "I'm happy to help."),
		seg(SpeakerCustomer, "Great, thank you!"),
	})
	if u.AlertCount != nil || *u.CallerSentimentTrend != "Up" || len(*u.SentimentHistory) != 3 {
		t.Errorf("alerts=%v (alerts come from categories) trend=%s history=%d", u.AlertCount, *u.CallerSentimentTrend, len(*u.SentimentHistory))
	}
	if (*u.SentimentHistory)[0].Time != "00:01" {
		t.Errorf("history time = %q", (*u.SentimentHistory)[0].Time)
	}
}

func TestSummarize(t *testing.T) {
	transcript := ScoreTranscript([]store.Segment{
		seg(SpeakerCustomer, "I need help with my bill."),
		seg(SpeakerCustomer, "This is unacceptable."),
		seg(SpeakerAgent, "I've refunded it."),
		seg(SpeakerCustomer, "Thanks!"),
	})
	c := store.Call{Agent: "BobS", Duration: "00:01:00", CallerSentiment: "Neutral"}
	got := Summarize(c, transcript, []string{"ManagerEscalation", "Billing"})
	for _, want := range []string{
		// Only what was said: no agent name or call length
		"**SUMMARY**\n\nThe caller said: \"I need help with my bill.\" Their main concern was: \"This is unacceptable.\" The caller ended with: \"Thanks!\"\n\n",
		"**DETAILS**\n\nThe topic of the call is manager escalation and billing.\n\n",
		// The caller's request is an action for the agent, then what the agent did
		"**ACTIONS**\n\n1. **Help with my bill**\n\nThe caller asked for this at",
		"2. **I've refunded it**\n\nSaid by the agent at",
		"**Owner:** Agent\n\n**Due Date:** TBD",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "BobS") || strings.Contains(got, "00:01:00") {
		t.Errorf("summary still names the agent or the call length:\n%s", got)
	}
}

// The screenshot's call: the caller's requests become the agent's actions
func TestRequestTitle(t *testing.T) {
	for in, want := range map[string]string{
		"I want to block my card. That's 10. Want to... Also open a new...": "Block my card",
		"Hi, I'm calling about my rewards card.":                            "Help with my rewards card",
		"I need help with my bill.":                                         "Help with my bill",
		"Can you please reset my password?":                                 "Reset my password",
		"I have lost my card. 18 point.":                                    "Help with: I have lost my card",
		"I was wondering about your life insurance policies.":               "Answer the caller's question about your life insurance policies",
	} {
		if got := requestTitle(in); got != want {
			t.Errorf("requestTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// Each request in a line is its own action
func TestRuleActionsSplitRequests(t *testing.T) {
	got := RuleActions([]store.Segment{seg(SpeakerCustomer, "I want to block my card. That's 10. Also open a new account.")})
	var titles []string
	for _, a := range got {
		titles = append(titles, a.Title)
	}
	if want := []string{"Block my card", "Open a new account"}; !slices.Equal(titles, want) {
		t.Errorf("actions = %q, want %q", titles, want)
	}
}

func TestCovered(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing []string
		want     bool
	}{
		{"Block my card", []string{"Block the caller's card"}, true},
		{"Open a new account", []string{"Block the caller's card"}, false},
		{"Card services", []string{"Lost card"}, true},
		{"Account opening", []string{"Lost card"}, false},
		{"Help with: I was charged twice", []string{"Refund the duplicate charge"}, true}, // charged ~ charge
		{"Help with my bill", nil, false},
	} {
		if got := Covered(tc.name, tc.existing); got != tc.want {
			t.Errorf("Covered(%q, %q) = %v", tc.name, tc.existing, got)
		}
	}
}

func TestNewCategories(t *testing.T) {
	transcript := []store.Segment{
		seg(SpeakerCustomer, "I have lost my card."),
		seg(SpeakerCustomer, "I want to block my card. Also open a new account."),
		seg(SpeakerCustomer, "And my parcel delivery is late."),
	}
	if got, want := Categories(transcript), []string{"CardServices", "AccountOpening", "Orders"}; !slices.Equal(got, want) {
		t.Errorf("Categories = %q, want %q", got, want)
	}
	// Topics without a tip add no assistant line
	if tips := Suggestions(transcript[1], nil); len(tips) != 0 {
		t.Errorf("tips = %q", tips)
	}
}

func TestSummarizeAgentOnly(t *testing.T) {
	transcript := []store.Segment{
		seg(SpeakerAgent, "Thanks for calling, how can I help?"),
		seg(SpeakerAgent, "I've updated your plan."),
	}
	got := Summarize(store.Call{Agent: "BobS", Duration: "00:00:30"}, transcript, []string{"Sales"})
	for _, want := range []string{"The agent opened with: \"Thanks for calling, how can I help?\"", "The topic of the call is sales.", "1. **I've updated your plan**"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
	if got := Summarize(store.Call{}, transcript, []string{"AgentGreeting"}); !strings.Contains(got, "The topic of the call is a general enquiry.") {
		t.Errorf("no-topic call summary:\n%s", got)
	}
	empty := Summarize(store.Call{}, nil, nil)
	if !strings.Contains(empty, "No speech was captured.") || !strings.Contains(empty, "The topic of the call is a general enquiry.") ||
		!strings.HasSuffix(empty, "**ACTIONS**\n\nNone") {
		t.Errorf("empty call summary:\n%s", empty)
	}
}

// Actions read back from a summary match the ones it was written from
func TestParseSummaryActions(t *testing.T) {
	actions := []SummaryAction{
		{Title: "Send the policy details", Detail: "Will email them.", Owner: "Agent", Due: "Friday"},
		{Title: "Confirm the refund", Owner: "TBD", Due: "TBD"},
	}
	got := ParseSummaryActions(FormatSummary(SummaryParts{Summary: "S.", Topic: "billing", Actions: actions}))
	if !slices.Equal(got, actions) {
		t.Errorf("ParseSummaryActions = %+v, want %+v", got, actions)
	}
	for _, other := range []string{"", FormatSummary(SummaryParts{Summary: "S."}), "**SUMMARY**\n\nOld layout, no actions."} {
		if got := ParseSummaryActions(other); len(got) != 0 {
			t.Errorf("ParseSummaryActions(%q) = %+v", other, got)
		}
	}
}

func TestFormatSummary(t *testing.T) {
	got := FormatSummary(SummaryParts{
		Summary: "The caller asked about\n their rewards card.",
		Topic:   "Life insurance.",
		Actions: []SummaryAction{
			{Title: "Provide information about the cashback rate.", Detail: "The agent explained the 2% rate.", Owner: "", Due: "none"},
			{Title: "Send the policy details", Owner: "Agent", Due: "Friday"},
		},
	})
	want := "**SUMMARY**\n\nThe caller asked about their rewards card.\n\n" +
		"**DETAILS**\n\nThe topic of the call is life insurance.\n\n" +
		"**ACTIONS**\n\n" +
		"1. **Provide information about the cashback rate**\n\nThe agent explained the 2% rate.\n\n**Owner:** TBD\n\n**Due Date:** TBD\n\n" +
		"2. **Send the policy details**\n\n**Owner:** Agent\n\n**Due Date:** Friday"
	if got != want {
		t.Errorf("FormatSummary =\n%s\nwant\n%s", got, want)
	}
	// Topics read as a sentence; acronyms keep their capitals
	for topic, want := range map[string]string{
		"VPN access": "VPN access", "Rewards Card and Life Insurance": "rewards card and life insurance",
	} {
		if got := FormatSummary(SummaryParts{Topic: topic}); !strings.Contains(got, "The topic of the call is "+want+".") {
			t.Errorf("topic %q:\n%s", topic, got)
		}
	}
	// Only real days and dates are kept as due dates
	for due, want := range map[string]string{"Friday": "Friday", "next week": "next week", "March 3": "March 3",
		"within 5 days": "within 5 days", "Call end": "TBD", "": "TBD", "N/A": "TBD"} {
		got := FormatSummary(SummaryParts{Actions: []SummaryAction{{Title: "Send it", Due: due}}})
		if !strings.HasSuffix(got, "**Due Date:** "+want) {
			t.Errorf("due %q:\n%s", due, got)
		}
	}
}

func TestScoreTranscript(t *testing.T) {
	kept := 3.0
	in := []store.Segment{
		{Speaker: SpeakerCustomer, Text: "This is terrible."},
		{Speaker: SpeakerAssistant, Text: "tip"},
		{Speaker: SpeakerAgent, Text: "Sorry about that", SentimentScore: &kept},
	}
	out := ScoreTranscript(in)
	if out[0].Sentiment == nil || *out[0].Sentiment != "negative" {
		t.Errorf("customer line = %+v", out[0])
	}
	if out[1].Sentiment != nil {
		t.Error("assistant tips must not be scored")
	}
	if *out[2].SentimentScore != kept {
		t.Error("existing score was overwritten")
	}
	if in[0].Sentiment != nil {
		t.Error("input transcript was modified")
	}
}

func TestIssueLabels(t *testing.T) {
	if got := MainIssue([]string{"Billing", "ManagerEscalation"}); !slices.Equal(got, []string{"Billing"}) {
		t.Errorf("MainIssue = %q", got)
	}
	if got := MainIssue([]string{"AgentGreeting"}); len(got) != 0 {
		t.Errorf("MainIssue of a general enquiry = %q", got)
	}
	got := IssueLabels([]string{"ManagerEscalation", "AgentGreeting", "Billing"})
	if want := []string{"Manager escalation", "Billing"}; !slices.Equal(got, want) {
		t.Errorf("IssueLabels = %q, want %q", got, want)
	}
	if got := IssueLabels([]string{"AgentGreeting"}); len(got) != 0 {
		t.Errorf("greeting-only call issues = %q", got)
	}
}

// Categories come from what the caller said, main topic first, and
// ambiguous words such as "order" in "in order to" or "plan" don't count
func TestCategoriesFromCaller(t *testing.T) {
	transcript := []store.Segment{
		seg(SpeakerAgent, "Thank you for calling, in order to help let me check your plan and any offers."),
		seg(SpeakerCustomer, "This is terrible, my card has been blocked for three days."),
		seg(SpeakerCustomer, "I was also charged a fee for it."),
		seg(SpeakerCustomer, "My card was blocked again this morning."),
		seg(SpeakerAgent, "I'll check your bill and the delivery of the new card."),
	}
	if got, want := Categories(transcript), []string{"CardServices", "Billing", "AgentGreeting"}; !slices.Equal(got, want) {
		t.Errorf("Categories = %q, want %q", got, want)
	}
	// An agent-only recording still gets its topics from the agent
	agentOnly := []store.Segment{seg(SpeakerAgent, "I can see the refund for your order number 4521.")}
	if got, want := Categories(agentOnly), []string{"Billing", "Orders"}; !slices.Equal(got, want) {
		t.Errorf("agent-only Categories = %q, want %q", got, want)
	}
	// The topic mentioned more often wins when two start on the same line
	tied := []store.Segment{
		seg(SpeakerCustomer, "I need help with a payment and an upgrade."),
		seg(SpeakerCustomer, "Is the upgrade available now?"),
		seg(SpeakerCustomer, "The upgrade is what I want."),
	}
	if got := Categories(tied); got[0] != "Sales" {
		t.Errorf("tied Categories = %q, want Sales first", got)
	}
}

// A frustrated caller isn't averaged to neutral by short replies, and one
// closing "thanks" doesn't make a plain call positive
func TestAggregateCallerLabel(t *testing.T) {
	frustrated := []store.Segment{
		seg(SpeakerCustomer, "Hi, my name is Sam"),
		seg(SpeakerCustomer, "This is terrible, my card has been blocked for three days"),
		seg(SpeakerCustomer, "Yes"),
		seg(SpeakerCustomer, "It is 4521"),
		seg(SpeakerCustomer, "This is ridiculous, I need it today"),
		seg(SpeakerCustomer, "Okay"),
		seg(SpeakerCustomer, "Fine, bye"),
	}
	if u := Aggregate(frustrated); *u.CallerSentiment != "Negative" {
		t.Errorf("frustrated caller = %s (%.2f), want Negative", *u.CallerSentiment, *u.CallerAvgSentiment)
	}
	// A caller getting angrier still trends down when they close with "okay"
	worsening := []store.Segment{
		seg(SpeakerCustomer, "My card has been blocked for three days and nobody has helped me."),
		seg(SpeakerCustomer, "Sam."),
		seg(SpeakerCustomer, "This is the third time I'm calling, I'm fed up with this."),
		seg(SpeakerCustomer, "It's still not working, this is ridiculous."),
		seg(SpeakerCustomer, "Okay."),
	}
	if u := Aggregate(worsening); *u.CallerSentimentTrend != "Down" {
		t.Errorf("worsening caller trend = %s, want Down", *u.CallerSentimentTrend)
	}
	plain := []store.Segment{
		seg(SpeakerCustomer, "Hi, I'd like to cancel my subscription"),
		seg(SpeakerCustomer, "Yes, the account number is 4521"),
		seg(SpeakerCustomer, "I was charged twice last month as well"),
		seg(SpeakerCustomer, "Okay"),
		seg(SpeakerCustomer, "Thank you, bye"),
	}
	if u := Aggregate(plain); *u.CallerSentiment != "Neutral" {
		t.Errorf("plain caller = %s (%.2f), want Neutral", *u.CallerSentiment, *u.CallerAvgSentiment)
	}
}

func TestRescoreTranscript(t *testing.T) {
	stale, label := -1.5, "negative"
	in := []store.Segment{
		{Speaker: SpeakerCustomer, Text: "I'd like to cancel my plan", SentimentScore: &stale, Sentiment: &label},
		{Speaker: SpeakerAssistant, Text: "tip"},
	}
	out := RescoreTranscript(in)
	if *out[0].Sentiment != "neutral" || *out[0].SentimentScore != 0 || out[1].SentimentScore != nil {
		t.Errorf("rescored = %+v", out)
	}
	if *in[0].SentimentScore != stale {
		t.Error("input was changed")
	}
}

// AgentGreeting is matched by the agent offering help, as in LCA, never filled
// in when nothing else matched; a call matching nothing has no categories
func TestGreetingCategory(t *testing.T) {
	if got := Categories([]store.Segment{seg(SpeakerCustomer, "Hello?"), seg(SpeakerAgent, "Hi, this is Sam.")}); len(got) != 0 {
		t.Errorf("categories with nothing matched = %q, want none", got)
	}
	if got := Categories([]store.Segment{seg(SpeakerCustomer, "How can I help you, I mean what can I do?")}); len(got) != 0 {
		t.Errorf("caller's words matched the agent greeting: %q", got)
	}
	got := Categories([]store.Segment{seg(SpeakerAgent, "Good morning, how may I help you?"), seg(SpeakerCustomer, "Can you charge my card back?")})
	if want := []string{"Billing", "AgentGreeting"}; !slices.Equal(got, want) {
		t.Errorf("categories = %q, want %q", got, want)
	}
}

func TestNewCategoriesAndAlerts(t *testing.T) {
	if got := NewCategories([]string{"Billing"}, []string{"Billing", "ManagerEscalation"}); !slices.Equal(got, []string{"ManagerEscalation"}) {
		t.Errorf("NewCategories = %q", got)
	}
	if got := NewCategories(nil, nil); len(got) != 0 {
		t.Errorf("NewCategories of nothing = %q", got)
	}
	cats := []string{"Billing", "ManagerEscalation", "Cancellation"}
	if n := AlertCount(cats, regexp.MustCompile("ManagerEscalation")); n != 1 {
		t.Errorf("alerts = %d, want 1", n)
	}
	if n := AlertCount(cats, regexp.MustCompile(".*")); n != 3 {
		t.Errorf("every category an alert = %d, want 3", n)
	}
	if n := AlertCount(cats, nil); n != 0 {
		t.Errorf("no pattern = %d", n)
	}
}

func TestInsuranceOpp(t *testing.T) {
	got := Categories([]store.Segment{seg(SpeakerCustomer, "I want to ask about my life insurance premium.")})
	if !slices.Contains(got, "InsuranceOpp") {
		t.Errorf("categories = %q, want InsuranceOpp", got)
	}
}
