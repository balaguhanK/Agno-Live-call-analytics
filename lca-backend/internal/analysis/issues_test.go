package analysis

import (
	"slices"
	"testing"

	"lca-backend/internal/store"
)

// The call from the screenshot, as transcribed
var reservationCall = []store.Segment{
	{ID: 1, Speaker: SpeakerCustomer, TimeStart: "00:00.1", Text: "Hello?"},
	{ID: 2, Speaker: SpeakerAgent, TimeStart: "00:02.8", Text: "And I'll have... Okay. Thank you."},
	{ID: 3, Speaker: SpeakerCustomer, TimeStart: "00:05.8", Text: "I'm really not satisfied with your reservation fee."},
	{ID: 4, Speaker: SpeakerAgent, TimeStart: "00:09.9", Text: "Can I know what is your problem?"},
	{ID: 5, Speaker: SpeakerCustomer, TimeStart: "00:14.6", Text: "I'm really frustrated."},
	{ID: 7, Speaker: SpeakerAgent, TimeStart: "00:15.9", Text: "Okay. For what reason, are interested?"},
	{ID: 8, Speaker: SpeakerCustomer, TimeStart: "00:20.7", Text: "I have lost my card, and I can't block it. I want to block it."},
	{ID: 10, Speaker: SpeakerAgent, TimeStart: "00:25.3", Text: "Okay, ma'am. I'm sorry hear that. I'll block your call right now. Then I ordered a question for you. I will provide you a link. On the thing, we can leave your details."},
	{ID: 11, Speaker: SpeakerCustomer, TimeStart: "00:35.7", Text: "Okay. Thank you."},
}

func TestIssueNameFromLine(t *testing.T) {
	for line, want := range map[string]string{
		"I'm really not satisfied with your reservation fee.":            "Reservation fee",
		"I have lost my card, and I can't block it. I want to block it.": "Lost card",
		"I want to cancel an order.":                                     "Order cancellation",
		"Also open a new account.":                                       "Account opening",
		"Hi, I'm calling about my rewards card.":                         "Rewards card",
		"I was charged twice this month.":                                "Duplicate charge",
		"I forgot my password and cannot sign in.":                       "Forgotten password",
		"There's a problem with my internet bill and it's slow":          "Internet bill",
		"My app is not working since yesterday":                          "App not working",
		"Hello?":                                                         "",
		"I'm really frustrated.":                                         "",
	} {
		if got := IssueNameFromLine(line); got != want {
			t.Errorf("IssueNameFromLine(%q) = %q, want %q", line, got, want)
		}
	}
}

// Both problems the caller raised are issues, in the order raised
func TestRuleIssuesFindsEveryIssue(t *testing.T) {
	if got, want := RuleIssues(reservationCall, []string{"Billing", "CardServices"}), []string{"Reservation fee", "Lost card"}; !slices.Equal(got, want) {
		t.Errorf("RuleIssues = %q, want %q", got, want)
	}
	// With no issue line, the main topic stands in
	if got := RuleIssues([]store.Segment{{Speaker: SpeakerCustomer, Text: "Hello?"}}, []string{"Billing", "Sales"}); !slices.Equal(got, []string{"Billing"}) {
		t.Errorf("RuleIssues with no issue line = %q", got)
	}
}

// Each promise the agent made is an action, in their words
func TestPromiseTitles(t *testing.T) {
	got := promiseTitles("Okay, ma'am. I'm sorry hear that. I'll block your call right now. Then I ordered a question for you. I will provide you a link. On the thing, we can leave your details.")
	if want := []string{"Block the customer's call right now", "Provide the customer a link"}; !slices.Equal(got, want) {
		t.Errorf("promiseTitles = %q, want %q", got, want)
	}
	acts := RuleActions(reservationCall)
	var titles []string
	for _, a := range acts {
		if IsFollowUp(a) {
			titles = append(titles, a.Title)
		}
	}
	// The caller's request and the agent's two promises; "I'll have..." is filler
	// and the complaints are issues, not actions
	if want := []string{"Block it", "Block the customer's call right now", "Provide the customer a link"}; !slices.Equal(titles, want) {
		t.Errorf("follow-ups = %q, want %q", titles, want)
	}
}

// The agent only said "I'll block your call right now": nothing may claim
// the card was blocked
func TestFixUnfinishedClaims(t *testing.T) {
	for in, want := range map[string]string{
		"A customer called to complain about a reservation fee and requested to block their lost card. The agent blocked the card and provided information on how to proceed.": "A customer called to complain about a reservation fee and requested to block their lost card. The agent said they would block the card and would provide information on how to proceed.",
		// What Gemma wrote for this call on a real run
		"A customer called to complain about a reservation fee and lost their card. The agent apologized and blocked the card, then provided a link for the customer to update their details.": "A customer called to complain about a reservation fee and lost their card. The agent apologized and would block the card, then would provide a link for the customer to update their details.",
		"The agent said they would block the customer's card and explained the loan policy.":                                                                                                   "The agent said they would block the customer's card and would explain the loan policy.",
		"Agent explained the personal loan options": "Agent said they would explain the personal loan options",
		"Blocked the customer's card":               "Will block the customer's card",
		"Block the customer's card":                 "Block the customer's card",
		"The customer requested a card":             "The customer requested a card",
	} {
		if got := FixUnfinishedClaims(in, reservationCall); got != want {
			t.Errorf("FixUnfinishedClaims(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	// What the agent said they did stays as it is
	done := append(slices.Clone(reservationCall), store.Segment{Speaker: SpeakerAgent, Text: "I've blocked your card now."})
	if got := FixUnfinishedClaims("The agent blocked the card.", done); got != "The agent blocked the card." {
		t.Errorf("done claim changed: %q", got)
	}
}

func TestTrimOrgLead(t *testing.T) {
	for in, want := range map[string]string{
		"Calling Acme Bank": "Acme Bank", "Welcome to Acme Bank": "Acme Bank",
		"Hello Acme Bank": "Acme Bank", "Acme Bank": "Acme Bank", "Bank": "Bank",
	} {
		if got := trimOrgLead(in); got != want {
			t.Errorf("trimOrgLead(%q) = %q, want %q", in, got, want)
		}
	}
}

// Asking for information is a request, not an issue
func TestInquiryIsNotAnIssue(t *testing.T) {
	for text, want := range map[string]string{
		"I want to know the personal loan":                          "",
		"Could you explain your loan policy and the steps?":         "",
		"I would like to know about your interest rates":            "",
		"I was wondering how much the processing fee is":            "",
		"I want to block my card":                                   store.InsightIssue,
		"I want to know why I was charged twice":                    store.InsightIssue,
		"I want a refund":                                           store.InsightIssue,
		"I'm calling because my card is not working":                store.InsightIssue,
		"I have a problem, can you explain what this charge is for": store.InsightIssue,
	} {
		if got := DetectInsight(SpeakerCustomer, text, 0); got != want {
			t.Errorf("DetectInsight(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestInquiryOnlyCallNamesAnInquiry(t *testing.T) {
	tr := []store.Segment{
		{ID: 1, Speaker: SpeakerAgent, Text: "Calling Acme Bank. How can I help you?"},
		{ID: 2, Speaker: SpeakerCustomer, Text: "I want to know the personal loan"},
	}
	cats := Categories(tr)
	if !slices.Contains(cats, "PersonalLoan") {
		t.Fatalf("categories = %q, want PersonalLoan", cats)
	}
	if got := RuleIssues(tr, cats); len(got) != 0 {
		t.Errorf("RuleIssues = %q, want none (inquiry is not an issue)", got)
	}
	if got := IssueLines(tr); len(got) != 0 {
		t.Errorf("an inquiry was flagged as an issue line: %+v", got)
	}
	// With a real problem as well, the problem is the issue
	tr = append(tr, store.Segment{ID: 3, Speaker: SpeakerCustomer, Text: "Also my card is blocked and I can't use it"})
	if got := RuleIssues(tr, Categories(tr)); slices.Contains(got, "Personal loan inquiry") || len(got) == 0 {
		t.Errorf("RuleIssues with a problem = %q", got)
	}
}

func TestGarbledInquiriesAreNotIssues(t *testing.T) {
	for _, text := range []string{
		"I also want to know about the question... About the question.",
		"I want to know about the person wrong.",
	} {
		if got := DetectInsight(SpeakerCustomer, text, 0); got != "" {
			t.Errorf("DetectInsight(%q) = %q, want none", text, got)
		}
	}
	if got := DetectInsight(SpeakerCustomer, "I want to know why this amount is wrong", 0); got != "" && got != store.InsightIssue {
		t.Errorf("unexpected label %q", got)
	}
}

func TestSpokenDigitsAreEntities(t *testing.T) {
	tr := []store.Segment{
		{ID: 1, Speaker: SpeakerCustomer, Text: "Nine eight four letter."},
		{ID: 2, Speaker: SpeakerCustomer, Text: "It ends with nine eight four two."},
		{ID: 3, Speaker: SpeakerCustomer, Text: "My number is nine eight seven six five four three two one zero."},
		{ID: 4, Speaker: SpeakerCustomer, Text: "I want one or two cards."},
	}
	got := Entities(tr)[store.EntityReference]
	if !slices.Equal(got, []string{"984", "9842"}) {
		t.Errorf("reference entities = %q, want [984 9842]", got)
	}
}

// A finished call that matched no rule still gets a category, from its issues or topic
func TestFallbackCategory(t *testing.T) {
	for _, tc := range []struct {
		issues []string
		topic  string
		want   string
	}{
		{[]string{"Lost credit card"}, "", "CardServices"},
		{[]string{"Personal loan inquiry"}, "", "PersonalLoan"},
		{nil, "personal loan", "PersonalLoan"},
		{[]string{"Something else entirely"}, "", GeneralCategory},
		{[]string{"Gadget noise"}, "personal loan", GeneralCategory},
		{nil, "", GeneralCategory},
	} {
		if got := FallbackCategory(tc.issues, tc.topic); got != tc.want {
			t.Errorf("FallbackCategory(%q, %q) = %q, want %q", tc.issues, tc.topic, got, tc.want)
		}
	}
	if HasTopicCategory([]string{"AgentGreeting"}) || !HasTopicCategory([]string{"AgentGreeting", "Billing"}) {
		t.Error("HasTopicCategory wrong")
	}
	if got := Categories([]store.Segment{{Speaker: SpeakerCustomer, Text: "I lost my ATM cards."}}); !slices.Contains(got, "CardServices") {
		t.Errorf("lost ATM cards matched %q, want CardServices", got)
	}
}

// Negative words make a line negative, thanks and praise make it positive,
// and plain statements and questions stay neutral
func TestSentimentFollowsTheWords(t *testing.T) {
	for text, want := range map[string]string{
		"I am frustrated": "negative", "I'm really angry with you": "negative", "I'm irritated": "negative",
		"This sucks": "negative", "I am so upset": "negative", "This is unacceptable": "negative",
		"I'm not happy with this": "negative", "I am fed up": "negative", "I'm furious": "negative",
		"Thank you": "positive", "Thanks": "positive", "Okay. Thank you.": "positive", "Thank you so much": "positive",
		"Cheers": "positive", "God bless you": "positive", "Much appreciated": "positive", "You have been very helpful": "positive",
		"Thanks, but this is ridiculous": "negative",
		"Hello?":                         "neutral", "I lost my card": "neutral", "I want to block it": "neutral",
		"Can you tell me the last four digits?": "neutral",
	} {
		if got := Label(Score(text)); got != want {
			t.Errorf("Label(Score(%q)) = %q (%.1f), want %q", text, got, Score(text), want)
		}
	}
	// A negated negative word is calm, not delighted
	if got := Score("I am not angry"); got <= 0 || got >= positiveScore {
		t.Errorf("Score(I am not angry) = %.1f, want a little above 0 and below positive", got)
	}
}
