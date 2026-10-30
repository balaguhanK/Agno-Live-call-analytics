package analysis

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"lca-backend/internal/store"
)

// ── Grounding: keeping model text true to the call ──────────
//
// A small model sometimes writes things nobody said. Every claim it makes
// about the call must point at a real transcript line (FindEvidence), and
// text it writes may only contain numbers and references that were said on
// the call (UnsupportedFacts).

// minEvidenceOverlap is the share of a quote's words that must appear in
// one transcript line for that line to count as its evidence: high enough
// to reject made-up quotes, low enough to allow a dropped filler word
const minEvidenceOverlap = 0.75

// A quote shorter than this many words can't identify a line on its own
// ("yes", "okay"), so it must match the whole line
const minEvidenceWords = 3

var numberWords = map[string]string{
	"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "seven": "7",
	"eight": "8", "nine": "9", "ten": "10", "eleven": "11", "twelve": "12", "fifteen": "15", "twenty": "20",
	"thirty": "30", "forty": "40", "fifty": "50", "hundred": "100",
}

// factRe finds the facts a sentence can get wrong: numbers (amounts, days,
// percentages, times) and reference codes such as QX123
var factRe = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9-]*\d[A-Za-z0-9-]*|\d+`)

// FindEvidence returns the transcript line, said by one of speakers, that
// quote comes from: either the quote appears in it word for word, or nearly
// all of the quote's words do. ok is false when no line supports it.
func FindEvidence(transcript []store.Segment, quote string, speakers ...string) (store.Segment, bool) {
	q := words(quote)
	if len(q) == 0 {
		return store.Segment{}, false
	}
	joined := " " + strings.Join(q, " ") + " "
	var best store.Segment
	bestScore := 0.0
	for _, s := range transcript {
		if len(speakers) > 0 && !contains(speakers, s.Speaker) {
			continue
		}
		line := words(s.Text)
		if len(q) < minEvidenceWords {
			if slices.Equal(q, line) {
				return s, true
			}
			continue
		}
		if strings.Contains(" "+strings.Join(line, " ")+" ", joined) {
			return s, true
		}
		have := map[string]bool{}
		for _, w := range line {
			have[w] = true
		}
		hits := 0
		for _, w := range q {
			if have[w] {
				hits++
			}
		}
		if score := float64(hits) / float64(len(q)); score > bestScore {
			best, bestScore = s, score
		}
	}
	return best, bestScore >= minEvidenceOverlap
}

// UnsupportedFacts lists the numbers and reference codes in text that were
// never said on the call: a sign the model made them up. Number words on
// the call ("three days") count for their digits ("3 days").
func UnsupportedFacts(text string, transcript []store.Segment) []string {
	said := map[string]bool{}
	for _, s := range transcript {
		if s.Speaker != SpeakerAgent && s.Speaker != SpeakerCustomer {
			continue
		}
		for _, f := range factRe.FindAllString(strings.ToLower(s.Text), -1) {
			said[f] = true
			// "$45.20" is said as 45.20; also accept its parts
			for _, part := range strings.FieldsFunc(f, func(r rune) bool { return r == '.' || r == ',' || r == '-' }) {
				said[part] = true
			}
		}
		for _, w := range words(s.Text) {
			if d, ok := numberWords[w]; ok {
				said[d] = true
			}
		}
	}
	var missing []string
	for _, f := range factRe.FindAllString(strings.ToLower(text), -1) {
		if said[f] {
			continue
		}
		// "3-5" or "45.20" is fine when each number in it was said
		ok := true
		for _, part := range strings.FieldsFunc(f, func(r rune) bool { return r == '.' || r == ',' || r == '-' }) {
			if !said[part] {
				ok = false
			}
		}
		if !ok {
			missing = append(missing, f)
		}
	}
	return missing
}

// Grounded reports whether text states no number or reference that wasn't
// said on the call.
func Grounded(text string, transcript []store.Segment) bool {
	return len(UnsupportedFacts(text, transcript)) == 0
}

func contains(xs []string, x string) bool { return slices.Contains(xs, x) }

// Things an agent does, past tense → plain form. "reset" isn't here: its
// past tense is the same word, so the tense can't be told
var doneVerbs = map[string]string{
	"blocked": "block", "unblocked": "unblock", "sent": "send", "emailed": "email", "refunded": "refund",
	"cancelled": "cancel", "canceled": "cancel", "issued": "issue", "processed": "process", "updated": "update",
	"ordered": "order", "provided": "provide", "replaced": "replace", "activated": "activate", "closed": "close",
	"opened": "open", "fixed": "fix", "resolved": "resolve", "scheduled": "schedule", "credited": "credit",
	"reversed": "reverse", "raised": "raise", "escalated": "escalate", "arranged": "arrange", "booked": "book",
	"applied": "apply", "waived": "waive", "changed": "change", "transferred": "transfer", "reactivated": "reactivate",
	"explained": "explain", "informed": "inform", "shared": "share", "described": "describe", "clarified": "clarify",
	"forwarded": "forward", "submitted": "submit", "completed": "complete", "approved": "approve", "delivered": "deliver",
	"registered": "register",
}

var (
	pastVerbAlt     = `(` + strings.Join(slices.Sorted(maps.Keys(doneVerbs)), "|") + `)`
	agentDidRe      = regexp.MustCompile(`(?i)\b(the agent|agent)\s+` + pastVerbAlt + `\b`)
	andDidRe        = regexp.MustCompile(`(?i)\b(and|then|also)\s+` + pastVerbAlt + `\b`)
	startsWithDidRe = regexp.MustCompile(`(?i)^\s*` + pastVerbAlt + `\b`)
	agentSentenceRe = regexp.MustCompile(`(?i)\b(?:the agent|agent)\b`)
)

// FixUnfinishedClaims rewrites what text says the agent did into what they
// said they would do, for each thing the call doesn't show was done: "The
// agent blocked the card" becomes "The agent said they would block the card"
// when the agent only said "I'll block your card". A detail starting with
// the deed ("Blocked the card") becomes "Will block the card". Done is when
// the agent's own words say so ("I've blocked it", "your card is blocked").
func FixUnfinishedClaims(text string, transcript []store.Segment) string {
	if text == "" {
		return text
	}
	var said strings.Builder
	for _, s := range transcript {
		if s.Speaker == SpeakerAgent {
			said.WriteString(" " + strings.Join(words(s.Text), " ") + " ")
		}
	}
	done := func(past string) bool { return strings.Contains(said.String(), " "+strings.ToLower(past)+" ") }
	base := func(past string) string { return doneVerbs[strings.ToLower(past)] }

	if m := startsWithDidRe.FindStringSubmatchIndex(text); m != nil && !done(text[m[2]:m[3]]) {
		text = text[:m[2]] + "Will " + base(text[m[2]:m[3]]) + text[m[3]:]
	}
	// Sentence by sentence, so "and ordered" is only changed in a sentence
	// about the agent ("The agent apologised and blocked the card")
	sentences := strings.SplitAfter(text, ". ")
	for i, sentence := range sentences {
		if !agentSentenceRe.MatchString(sentence) {
			continue
		}
		sentence = agentDidRe.ReplaceAllStringFunc(sentence, func(m string) string {
			sub := agentDidRe.FindStringSubmatch(m)
			if done(sub[2]) {
				return m
			}
			return sub[1] + " said they would " + base(sub[2])
		})
		sentence = andDidRe.ReplaceAllStringFunc(sentence, func(m string) string {
			sub := andDidRe.FindStringSubmatch(m)
			if done(sub[2]) {
				return m
			}
			return sub[1] + " would " + base(sub[2])
		})
		sentences[i] = sentence
	}
	return strings.Join(sentences, "")
}
