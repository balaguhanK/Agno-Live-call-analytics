package analysis

import (
	"fmt"
	"regexp"
	"strings"

	"lca-backend/internal/store"
)

// ── Call summary: SUMMARY, DETAILS, ACTIONS ─────────────────

// TBD fills an action's owner or due date when the call didn't say
const TBD = "TBD"

// SummaryAction is one thing the agent did or agreed to do on the call; it
// is also stored with the call's analysis.
type SummaryAction = store.ActionItem

// SummaryParts are the pieces of a call summary, written by the chat model
// or worked out from the transcript.
type SummaryParts struct {
	Summary string
	Topic   string // e.g. "life insurance"; DETAILS is one line built from it
	Actions []SummaryAction
}

// FormatSummary writes the call summary in the same Markdown layout every
// time, whoever wrote the parts.
func FormatSummary(p SummaryParts) string {
	var b strings.Builder
	b.WriteString("**SUMMARY**\n\n")
	b.WriteString(oneLine(p.Summary))
	b.WriteString("\n\n**DETAILS**\n\n")
	topic := strings.TrimRight(oneLine(p.Topic), ".")
	if topic == "" {
		topic = "a general enquiry"
	}
	fmt.Fprintf(&b, "The topic of the call is %s.", lowerWords(topic))
	b.WriteString("\n\n**ACTIONS**\n\n")
	if len(p.Actions) == 0 {
		b.WriteString("None")
		return b.String()
	}
	for i, a := range p.Actions {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%d. **%s**\n\n", i+1, strings.TrimRight(oneLine(a.Title), "."))
		if d := oneLine(a.Detail); d != "" {
			b.WriteString(d + "\n\n")
		}
		fmt.Fprintf(&b, "**Owner:** %s\n\n**Due Date:** %s", orTBD(a.Owner), dueDate(a.Due))
	}
	return b.String()
}

// TidyActions cleans actions the way the summary shows them: one-line
// text, no full stop after the title, TBD for an unknown owner or due date.
func TidyActions(actions []SummaryAction) []SummaryAction {
	out := make([]SummaryAction, len(actions))
	for i, a := range actions {
		out[i] = SummaryAction{
			Title: strings.TrimRight(oneLine(a.Title), "."), Detail: oneLine(a.Detail),
			Owner: orTBD(a.Owner), Due: dueDate(a.Due), Evidence: a.Evidence,
		}
	}
	return out
}

var (
	actionTitleRe = regexp.MustCompile(`^\d+\. \*\*(.+)\*\*$`)
	actionOwnerRe = regexp.MustCompile(`^\*\*Owner:\*\* (.*)$`)
	actionDueRe   = regexp.MustCompile(`^\*\*Due Date:\*\* (.*)$`)
)

// ParseSummaryActions reads the actions back out of a summary written by
// FormatSummary, for calls analysed before actions were stored separately.
// Summaries in any other layout give none.
func ParseSummaryActions(summary string) []SummaryAction {
	_, section, ok := strings.Cut(summary, "**ACTIONS**")
	if !ok {
		return nil
	}
	var actions []SummaryAction
	for _, para := range strings.Split(section, "\n\n") {
		para = strings.TrimSpace(para)
		switch m := actionTitleRe.FindStringSubmatch(para); {
		case m != nil:
			actions = append(actions, SummaryAction{Title: m[1]})
		case len(actions) == 0:
			continue
		case actionOwnerRe.MatchString(para):
			actions[len(actions)-1].Owner = actionOwnerRe.FindStringSubmatch(para)[1]
		case actionDueRe.MatchString(para):
			actions[len(actions)-1].Due = actionDueRe.FindStringSubmatch(para)[1]
		case para != "" && actions[len(actions)-1].Detail == "":
			actions[len(actions)-1].Detail = para
		}
	}
	return actions
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func orTBD(s string) string {
	if s = oneLine(s); s == "" || strings.EqualFold(s, "none") || strings.EqualFold(s, "unknown") || strings.EqualFold(s, "n/a") {
		return TBD
	}
	return s
}

// lowerWords lower-cases the topic's words, keeping acronyms such as "VPN",
// so DETAILS reads as one sentence.
func lowerWords(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) < 2 || strings.ToUpper(w) != w {
			words[i] = strings.ToLower(w)
		}
	}
	return strings.Join(words, " ")
}

// dueDate keeps a due date only when it names a day or date ("Friday",
// "March 3", "next week"); anything else, such as "Call end", is TBD.
func dueDate(s string) string {
	if s = orTBD(s); s != TBD && !dateRe.MatchString(s) && !strings.ContainsAny(s, "0123456789") {
		return TBD
	}
	return s
}

// Summarize writes the call summary from the transcript alone, for when no
// chat model writes it: the caller's words, the topic from the categories
// and the agent's actions and outcomes as the actions.
func Summarize(c store.Call, transcript []store.Segment, categories []string) string {
	return FormatSummary(RuleSummaryParts(c, transcript, categories))
}

// RuleSummaryParts are the summary's parts worked out from the transcript
// alone. Everything in them was said on the call, so they also stand in for
// any part a model got wrong.
func RuleSummaryParts(_ store.Call, transcript []store.Segment, categories []string) SummaryParts {
	var customer, agent []store.Segment
	for _, s := range transcript {
		switch s.Speaker {
		case SpeakerCustomer:
			customer = append(customer, s)
		case SpeakerAgent:
			agent = append(agent, s)
		}
	}

	// Only what was said: who handled the call and for how long are on the
	// page already
	var sum strings.Builder
	switch {
	case len(customer) == 0 && len(agent) == 0:
		sum.WriteString("No speech was captured.")
	case len(customer) == 0:
		// Agent-only recording: customer audio isn't captured
		fmt.Fprintf(&sum, "The agent opened with: %q", agent[0].Text)
	default:
		opening := customer[0]
		for _, s := range customer {
			if s.Insight == store.InsightIssue {
				opening = s
				break
			}
		}
		fmt.Fprintf(&sum, "The caller said: %q", opening.Text)
		worst := customer[0]
		for _, s := range customer[1:] {
			if score(s) < score(worst) {
				worst = s
			}
		}
		if score(worst) <= negativeScore && worst.Text != opening.Text {
			fmt.Fprintf(&sum, " Their main concern was: %q", worst.Text)
		}
		if last := customer[len(customer)-1]; last.Text != opening.Text {
			fmt.Fprintf(&sum, " The caller ended with: %q", last.Text)
		}
	}

	// With no topic matched, FormatSummary says "a general enquiry"
	return SummaryParts{Summary: sum.String(), Topic: strings.Join(IssueLabels(categories), " and "), Actions: RuleActions(transcript)}
}

// maxRuleActions caps the actions worked out from the transcript
const maxRuleActions = 4

// RuleActions lists, in call order, what the agent did or promised and what
// the caller asked for, as the call's actions, for when no chat model writes
// them. A caller's request ("I want to block my card") is the agent's to
// follow up.
func RuleActions(transcript []store.Segment) []SummaryAction {
	actions := []SummaryAction{}
	issues := 0
	for _, s := range transcript {
		if (s.Speaker != SpeakerAgent && s.Speaker != SpeakerCustomer) || len(actions) >= maxRuleActions {
			continue
		}
		insight := s.Insight
		if insight == "" {
			insight = DetectInsight(s.Speaker, s.Text, issues)
		}
		quote := &store.Quote{Time: minuteSecond(s.TimeStart), Text: s.Text}
		switch insight {
		case store.InsightOutcome:
			actions = append(actions, SummaryAction{
				Title: s.Text, Detail: "Said by the agent at " + minuteSecond(s.TimeStart) + ".", Owner: "Agent", Evidence: quote,
			})
		case store.InsightAction:
			// Each promise in the line is its own action: "I'll block your card
			// right now. I will provide you a link." is two
			// A line with no real promise in it ("And I'll have... Okay.") adds none
			for _, title := range promiseTitles(s.Text) {
				if len(actions) < maxRuleActions {
					actions = append(actions, SummaryAction{
						Title: title, Detail: promiseDetail + minuteSecond(s.TimeStart) + ".", Owner: "Agent", Evidence: quote,
					})
				}
			}
		case store.InsightIssue:
			issues++
			// "I want to block my card. Also open a new account." is two requests
			// A statement of the problem ("I have lost my card") is an issue,
			// shown under Issues, not an action
			for _, request := range requests(s.Text) {
				title := requestTitle(request)
				if !strings.HasPrefix(title, "Help with: ") && len(actions) < maxRuleActions {
					actions = append(actions, SummaryAction{
						Title: title, Detail: "The caller asked for this at " + minuteSecond(s.TimeStart) + ".", Owner: "Agent", Evidence: quote,
					})
				}
			}
		}
	}
	return actions
}

// requests splits a caller's line into the sentences that ask for something.
func requests(text string) []string {
	var out []string
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool { return r == '.' || r == '?' || r == '!' }) {
		if DetectInsight(SpeakerCustomer, sentence, 0) == store.InsightIssue {
			out = append(out, strings.TrimSpace(sentence))
		}
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

// Words that say nothing about what an action or issue is
var vagueWords = map[string]bool{
	"the": true, "a": true, "an": true, "my": true, "your": true, "their": true, "caller's": true, "customer's": true,
	"and": true, "to": true, "for": true, "of": true, "with": true, "help": true, "about": true, "new": true, "on": true,
}

// Covered reports whether name shares a meaningful word with one of
// existing ("Block my card" is covered by "Block the caller's card"; "Open a
// new account" isn't), so what a model left out can be added back.
func Covered(name string, existing []string) bool {
	keys := map[string]bool{}
	for _, w := range words(name) {
		if !vagueWords[w] {
			keys[stem(w)] = true
		}
	}
	for _, e := range existing {
		for _, w := range words(e) {
			if keys[stem(w)] {
				return true
			}
		}
	}
	return false
}

// stem drops common endings so "charged", "charges" and "charge" match.
func stem(w string) string {
	for _, end := range []string{"ing", "ed", "es", "s", "e"} {
		if len(w) > len(end)+2 && strings.HasSuffix(w, end) {
			return strings.TrimSuffix(w, end)
		}
	}
	return w
}

// promiseDetail starts the detail of an action the agent promised
const promiseDetail = "The agent said they will do this at "

// minPromiseWords is the fewest words a promise needs: a deed and its object
const minPromiseWords = 2

// Openings of an agent's promise: "I'll", "I will", "We're going to"
var promiseOpenings = []string{
	"i'll ", "i will ", "i'm going to ", "i am going to ", "we'll ", "we will ", "i can ", "let me ",
}

// promiseTitles turns each promise in an agent's line into a short action:
// "I will provide you a link." → "Provide the customer a link".
func promiseTitles(text string) []string {
	var out []string
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool { return r == '.' || r == '?' || r == '!' }) {
		sentence = strings.TrimSpace(sentence)
		lower := strings.ReplaceAll(strings.ToLower(sentence), "’", "'")
		for _, lead := range []string{"then ", "and ", "so ", "okay, ", "ok, "} {
			if strings.HasPrefix(lower, lead) {
				sentence, lower = sentence[len(lead):], lower[len(lead):]
			}
		}
		for _, opening := range promiseOpenings {
			if !strings.HasPrefix(lower, opening) {
				continue
			}
			rest := strings.TrimSpace(sentence[len(opening):])
			// Said to the customer, so "you" is the customer
			rest = strings.NewReplacer(" your ", " the customer's ", " you ", " the customer ").Replace(" " + rest + " ")
			// A promise names a deed and what it's about; "I'll have..." is filler
			if rest = strings.TrimSpace(rest); len(strings.Fields(rest)) >= minPromiseWords {
				out = append(out, strings.ToUpper(rest[:1])+rest[1:])
			}
			break
		}
	}
	return out
}

// IsFollowUp reports whether an action is still to do: something the caller
// asked for or the agent promised, as opposed to something already done.
func IsFollowUp(a SummaryAction) bool {
	return IsRequest(a) || strings.HasPrefix(a.Detail, promiseDetail)
}

// IsRequest reports whether an action is something the caller asked for,
// phrased as an action ("Open a new account"), rather than a statement of
// their problem ("Help with: I was charged twice"), which is an issue.
func IsRequest(a SummaryAction) bool {
	return strings.HasPrefix(a.Detail, "The caller asked") && !strings.HasPrefix(a.Title, "Help with: ")
}

// Openings of a caller's request and what replaces them, leaving a short
// action: "I want to block my card" → "Block my card", "I'm calling about
// my bill" → "Help with my bill". Longer openings come first.
var requestOpenings = []struct{ opening, replace string }{
	{"i need some help with", "Help with "}, {"i need help with", "Help with "},
	{"i'm calling about", "Help with "}, {"i am calling about", "Help with "}, {"i'm calling regarding", "Help with "},
	{"i have a question about", "Answer the caller's question about "}, {"i was wondering about", "Answer the caller's question about "},
	{"i'm calling to", ""}, {"i am calling to", ""}, {"i would like to", ""}, {"i'd like to", ""},
	{"i want to", ""}, {"i need to", ""}, {"i wanted to", ""}, {"can you please", ""}, {"could you please", ""},
	{"can you", ""}, {"could you", ""}, {"please", ""},
}

// First words of a sentence that describes the caller's situation rather
// than asking for something
var statementStarts = map[string]bool{
	"i": true, "i'm": true, "i've": true, "i'd": true, "i'll": true, "my": true, "we": true, "we're": true, "our": true,
	"it": true, "it's": true, "there": true, "there's": true, "this": true, "that": true, "the": true, "your": true,
	"you": true, "he": true, "she": true, "they": true, "is": true, "was": true, "am": true,
}

// requestTitle turns the caller's request line into a short action:
// "Hi, I want to block my card. That's 10." → "Block my card". A line that
// isn't phrased as a request becomes "Help with: I have lost my card".
func requestTitle(text string) string {
	sentence := text
	for _, sep := range []string{". ", "? ", "! "} {
		// The sentence holding the request, not small talk around it
		for _, part := range strings.Split(sentence, sep) {
			if DetectInsight(SpeakerCustomer, part, 0) == store.InsightIssue {
				sentence = part
				break
			}
		}
	}
	sentence = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sentence), ".?!… "))
	lower := strings.ToLower(sentence)
	for _, greeting := range []string{"hi, ", "hello, ", "hi ", "hello ", "yes, ", "so ", "also ", "and "} {
		if strings.HasPrefix(lower, greeting) {
			sentence, lower = sentence[len(greeting):], lower[len(greeting):]
		}
	}
	lower = strings.ReplaceAll(lower, "’", "'")
	for _, r := range requestOpenings {
		if !strings.HasPrefix(lower, r.opening+" ") {
			continue
		}
		rest := strings.TrimSpace(sentence[len(r.opening):])
		if rest == "" {
			break
		}
		if r.replace != "" {
			return r.replace + rest
		}
		return strings.ToUpper(rest[:1]) + rest[1:]
	}
	// Already a request ("open a new account"), not a statement about the
	// caller ("I have lost my card", "my card is blocked")
	first, _, _ := strings.Cut(lower, " ")
	if sentence != "" && !statementStarts[first] {
		return strings.ToUpper(sentence[:1]) + sentence[1:]
	}
	return "Help with: " + sentence
}

// IsInquiryCategory reports whether a category is an information request or general inquiry rather than an issue.
func IsInquiryCategory(c string) bool {
	return c == "PersonalLoan" || c == "AccountOpening"
}

// IssueLabels names the call's issues from its categories, e.g.
// "Cancellation" or "Manager escalation", for when no model names them.
func IssueLabels(categories []string) []string {
	labels := []string{}
	for _, c := range categories {
		if !IsGreeting(c) && !IsInquiryCategory(c) {
			labels = append(labels, splitCamel(c))
		}
	}
	return labels
}

// MainIssue names the call's main issue from its categories, which come
// main topic first, for when no model names it: at most one label.
func MainIssue(categories []string) []string {
	labels := IssueLabels(categories)
	if len(labels) > 1 {
		labels = labels[:1]
	}
	return labels
}
