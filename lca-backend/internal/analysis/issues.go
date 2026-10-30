package analysis

import (
	"regexp"
	"strings"

	"lca-backend/internal/store"
)

// ── Issue names from the caller's own words ─────────────────
//
// Every line where the caller raises a problem gets a short name built from
// what they said ("not satisfied with your reservation fee" → "Reservation
// fee"), so a call with two problems shows two issues, and no name is made
// up.

// An action on something and the noun its issue is named with:
// "cancel an order" → "Order cancellation", "block my card" → "Card block"
var actionNouns = map[string]string{
	"block": "block", "unblock": "unblock", "cancel": "cancellation", "change": "change", "update": "update",
	"close": "closure", "open": "opening", "reset": "reset", "activate": "activation", "replace": "replacement",
	"return": "return", "renew": "renewal", "upgrade": "upgrade", "transfer": "transfer", "track": "tracking",
}

var (
	// "not satisfied with your reservation fee", "a problem with my bill",
	// "calling about my rewards card": the thing the issue is about
	aboutRe = regexp.MustCompile(`(?i)\b(?:not (?:satisfied|happy|okay|ok) with|unhappy with|dissatisfied with|disappointed with|upset about|upset with|complain(?:ing)? about|complaint about|problem with|problems with|issue with|issues with|trouble with|calling about|calling regarding|question about|asking about|enquire about|inquire about|wondering about|help with)\s+(?:(?:my|your|the|this|our|an|a)\s+)?((?:[a-z]+\s?){1,3})`)
	// "lost my card", "stolen card"
	lostRe = regexp.MustCompile(`(?i)\b(lost|stolen|forgot|forgotten|damaged|broken)\s+(?:(?:my|the|our|an|a)\s+)?([a-z]+)`)
	// "cancel an order", "block my card", "open a new account"
	actionRe       = regexp.MustCompile(`(?i)\b(block|unblock|cancel|change|update|close|open|reset|activate|replace|return|renew|upgrade|transfer|track)\s+(?:(?:my|the|our|your|this|an|a)\s+)?(?:new\s+)?([a-z]+)`)
	chargedTwiceRe = regexp.MustCompile(`(?i)\b(?:charged twice|double charged|duplicate charge|charged two times)\b`)
	refundRe       = regexp.MustCompile(`(?i)\brefund\b`)
	notWorkingRe   = regexp.MustCompile(`(?i)\b(?:my|the)?\s*([a-z]+)\s+(?:is|isn't|is not|keeps|has)\s*(?:not working|stopped working|broken|crashing|down)\b|\b([a-z]+)\s+(?:not working|stopped working|keeps crashing)\b`)
)

// Words that end a thing's name ("reservation fee and" → "reservation fee")
var nameStops = map[string]bool{
	"and": true, "but": true, "or": true, "so": true, "because": true, "it": true, "that": true, "which": true,
	"please": true, "today": true, "now": true, "again": true, "is": true, "was": true, "for": true, "to": true,
	"i": true, "you": true, "we": true, "they": true, "this": true, "please,": true,
}

// IssueNameFromLine names the problem a caller's line raises, from their
// own words, or "" when the line doesn't say what it is about.
func IssueNameFromLine(text string) string {
	switch {
	case chargedTwiceRe.MatchString(text):
		return "Duplicate charge"
	}
	if m := aboutRe.FindStringSubmatch(text); m != nil {
		if name := thingName(m[1]); name != "" {
			return name
		}
	}
	if m := lostRe.FindStringSubmatch(text); m != nil && !nameStops[strings.ToLower(m[2])] {
		verb := strings.ToLower(m[1])
		if verb == "forgot" || verb == "forgotten" {
			return "Forgotten " + strings.ToLower(m[2])
		}
		return capitalise(verb + " " + strings.ToLower(m[2]))
	}
	if m := actionRe.FindStringSubmatch(text); m != nil && !nameStops[strings.ToLower(m[2])] {
		return capitalise(strings.ToLower(m[2]) + " " + actionNouns[strings.ToLower(m[1])])
	}
	if refundRe.MatchString(text) {
		return "Refund"
	}
	if m := notWorkingRe.FindStringSubmatch(text); m != nil {
		thing := m[1] + m[2]
		if thing != "" && !nameStops[strings.ToLower(thing)] {
			return capitalise(strings.ToLower(thing) + " not working")
		}
	}
	return ""
}

// thingName keeps the words of a thing's name up to the first word that
// ends it: "reservation fee and" → "Reservation fee".
func thingName(s string) string {
	var kept []string
	for _, w := range strings.Fields(strings.ToLower(s)) {
		if nameStops[w] {
			break
		}
		kept = append(kept, w)
	}
	return capitalise(strings.Join(kept, " "))
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// JoinTopics lists issue names as one topic: "reservation fee and lost
// card", "a, b and c".
func JoinTopics(names []string) string {
	lower := make([]string, len(names))
	for i, n := range names {
		lower[i] = strings.ToLower(n)
	}
	if len(lower) < 2 {
		return strings.Join(lower, "")
	}
	return strings.Join(lower[:len(lower)-1], ", ") + " and " + lower[len(lower)-1]
}

// IssueLines returns the caller's lines that raise an issue, in call order:
// the ones flagged during the call, or found again with the current phrases.
func IssueLines(transcript []store.Segment) []store.Segment {
	var out []store.Segment
	for _, s := range transcript {
		if s.Speaker != SpeakerCustomer || len(out) >= MaxIssues {
			continue
		}
		if s.Insight == store.InsightIssue || DetectInsight(s.Speaker, s.Text, len(out)) == store.InsightIssue {
			out = append(out, s)
		}
	}
	return out
}

// IssueNamesFromLine names each problem a caller's line raises, sentence by
// sentence: "I want to block my card. Also open a new account." → "Card
// block", "Account opening".
func IssueNamesFromLine(text string) []string {
	var names []string
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool { return r == '.' || r == '?' || r == '!' }) {
		if IsInquiry(sentence) {
			continue // asking for information isn't an issue
		}
		if name := IssueNameFromLine(sentence); name != "" && !Covered(name, names) {
			names = append(names, name)
		}
	}
	return names
}

// RuleIssues names each issue the caller raised, from their own words, in
// the order they raised them; with none, the call's main topic if it is a problem.
func RuleIssues(transcript []store.Segment, categories []string) []string {
	var names []string
	for _, s := range IssueLines(transcript) {
		for _, name := range IssueNamesFromLine(s.Text) {
			if !Covered(name, names) && len(names) < MaxIssues && !IsInquiryName(name) {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		// An inquiry is not an issue: calls asking for information have no issues
		if HasInquiry(transcript) && len(IssueLabels(categories)) == 0 {
			return nil
		}
		if main := MainIssue(categories); len(main) > 0 && !IsInquiryName(main[0]) {
			return main
		}
		return nil
	}
	return names
}
