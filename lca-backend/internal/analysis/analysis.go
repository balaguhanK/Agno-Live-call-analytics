// Package analysis turns transcript text into call analytics without any
// external service: word-list sentiment, personal-data redaction, keyword
// categories, live-assist suggestions and an extractive summary.
package analysis

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"lca-backend/internal/store"
)

const (
	SpeakerAgent     = "AGENT"
	SpeakerCustomer  = "CUSTOMER"
	SpeakerAssistant = "AGENT_ASSISTANT"
	// A transcript line saying a call category was just matched, as in LCA
	SpeakerCategory = "CATEGORY_MATCH"

	// Scores are clamped to this range, matching the dashboard's -5..+5 scale
	maxScore = 5.0
	// Segment scores at or beyond these map to positive / negative labels
	positiveScore = 1.0
	negativeScore = -1.0
	// A customer line at or below this score gets the frustration tip
	AlertScore = -2.5
	// The latest score must move this far from the running average to count as a trend
	trendDelta = 0.5
	// Words after a negator whose sentiment is flipped
	negationWindow   = 3
	intensifierBoost = 1.5
	// How much of a negative word's strength is left as praise once it is negated ("not bad")
	negatedNegativeWeight = 0.3
	// How much a neutral line counts in a call's average, against 1 for a
	// positive or negative one
	neutralLineWeight = 0.25
)

// ── Sentiment ───────────────────────────────────────────────

// Emotion words only. Words that name the reason for a call, such as
// "cancel", "refund", "charged", "locked", "error" or "problem", are left
// out: "I'd like to cancel my plan" is a neutral request, not a complaint.
var lexicon = map[string]float64{
	"great": 2, "good": 1, "perfect": 2.5, "excellent": 2.5, "awesome": 2.5, "brilliant": 2.5, "amazing": 2.5,
	"fantastic": 2.5, "happy": 2, "glad": 1.5, "appreciate": 2, "appreciated": 2, "helpful": 1.5, "love": 2.5,
	"wonderful": 2.5, "fine": 0.5, "resolved": 1.5, "fixed": 1.5, "solved": 1.5, "easy": 1, "quick": 1,
	"quickly": 1, "nice": 1.5, "pleased": 2, "satisfied": 2, "relieved": 2, "grateful": 2, "lovely": 2,
	"sure": 0.5, "absolutely": 1, "welcome": 1, "sorted": 1, "works": 1, "brilliantly": 2.5,
	"thanks": 1.5, "thank": 1.5,
	"bad": -1.5, "terrible": -3, "awful": -3, "horrible": -3, "worst": -3.5, "hate": -3, "angry": -3,
	"furious": -3.5, "mad": -2.5, "frustrated": -2.5, "frustrating": -2.5, "annoyed": -2, "annoying": -2,
	"upset": -2, "disappointed": -2, "disappointing": -2, "unhappy": -2.5, "dissatisfied": -2.5,
	"unsatisfied": -2.5, "unacceptable": -3, "ridiculous": -2.5, "useless": -2.5, "pathetic": -3,
	"rubbish": -2.5, "disgusting": -3, "disgraceful": -3, "appalling": -3, "shocking": -2.5,
	"outrageous": -3, "rude": -2.5, "incompetent": -3, "hopeless": -2.5, "nightmare": -2.5, "joke": -2,
	"scam": -3, "nonsense": -2, "lied": -2.5, "lying": -2.5, "poor": -1.5, "worse": -2, "complaint": -2,
	"worried": -1.5, "stressed": -2, "confused": -0.5, "unbelievable": -1.5, "wasted": -2,
	"unfortunately": -0.5,
	// More ways callers voice anger and annoyance
	"irritated": -2, "irritating": -2, "irritation": -2, "sucks": -2.5, "garbage": -3, "trash": -2.5,
	"pissed": -3, "livid": -3.5, "outraged": -3, "infuriating": -3, "exasperated": -2.5, "horrendous": -3,
	"dreadful": -3, "stupid": -2.5, "idiot": -3, "cheated": -2.5, "cheating": -2.5, "ignored": -2,
	"ignoring": -2, "unprofessional": -2.5, "careless": -2, "disgusted": -3, "mess": -1.5, "fuming": -3.5,
	// More ways callers thank and praise
	"thankyou": 1.5, "thx": 1, "cheers": 1.5, "bless": 1.5, "thankful": 2, "superb": 2.5, "kudos": 2,
	"impressed": 2, "impressive": 2, "outstanding": 2.5, "delighted": 2.5, "pleasure": 1.5, "friendly": 1.5,
	"polite": 1.5, "courteous": 1.5, "smooth": 1, "convenient": 1,
}

// Multi-word expressions of frustration that no single word carries
var phrases = map[string]float64{
	"fed up": -2.5, "sick of": -2.5, "tired of": -2, "had enough": -2.5, "waste of time": -3,
	"on hold for": -1.5, "been on hold": -1.5, "still waiting": -1.5, "still not": -1.5,
	"third time": -2, "second time": -1.5, "every time": -1, "again and again": -2,
	"not working": -1, "isn't working": -1, "doesn't work": -1, "stopped working": -1,
	"no one": -1, "nobody": -1, "nothing has been": -1.5, "not good enough": -2.5,
	"thanks for nothing": -3, "no thanks to": -2, "thank god": 1.5,
}

var negators = map[string]bool{"not": true, "no": true, "never": true, "don't": true, "doesn't": true,
	"didn't": true, "isn't": true, "wasn't": true, "aren't": true, "weren't": true, "can't": true,
	"cannot": true, "won't": true, "haven't": true, "hasn't": true, "couldn't": true, "wouldn't": true,
	// Speech-to-text often drops the apostrophe
	"dont": true, "doesnt": true, "didnt": true, "isnt": true, "wasnt": true, "cant": true, "havent": true}

// "No thanks" and "no, thank you" stay polite rather than turning negative
var unnegated = map[string]bool{"thanks": true, "thank": true, "sure": true, "welcome": true}

var intensifiers = map[string]bool{"very": true, "really": true, "so": true, "extremely": true, "totally": true,
	"absolutely": true, "completely": true}

// Words after "but" count, those before it count for this much: in "thanks,
// but this is ridiculous" the caller means the second half
const beforeButWeight = 0.5

func words(text string) []string {
	text = strings.NewReplacer("’", "'", "‘", "'").Replace(text)
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '\''
	})
}

// Score rates text from -5 (very negative) to +5 (very positive).
func Score(text string) float64 {
	var total float64
	negateLeft, boost := 0, 1.0
	ws := words(text)
	for _, w := range ws {
		if w == "but" {
			total *= beforeButWeight
			negateLeft, boost = 0, 1
			continue
		}
		if negators[w] {
			negateLeft = negationWindow
			continue
		}
		if intensifiers[w] {
			boost = intensifierBoost
			continue
		}
		if v, ok := lexicon[w]; ok {
			v *= boost
			if negateLeft > 0 && !unnegated[w] {
				v = -v
				// "I am not angry" is calm, not delighted
				if v > 0 {
					v *= negatedNegativeWeight
				}
			}
			total += v
		}
		boost = 1
		if negateLeft > 0 {
			negateLeft--
		}
	}
	padded := " " + strings.Join(ws, " ") + " "
	for p, v := range phrases {
		if strings.Contains(padded, " "+p+" ") {
			total += v
		}
	}
	return max(-maxScore, min(maxScore, total))
}

// Label maps a score to the segment label: positive, neutral or negative.
func Label(score float64) string {
	switch {
	case score >= positiveScore:
		return "positive"
	case score <= negativeScore:
		return "negative"
	default:
		return "neutral"
	}
}

func titleLabel(score float64) string {
	l := Label(score)
	return strings.ToUpper(l[:1]) + l[1:]
}

// ── Redaction ───────────────────────────────────────────────

var (
	emailRe = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)
	// 13-19 digits, optionally grouped with spaces or dashes
	cardRe  = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)
	phoneRe = regexp.MustCompile(`\+?\d[\d ().-]{7,}\d`)
	ssnRe   = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
)

// Redact replaces personal and card data with placeholders and returns the
// kinds it found, e.g. ["CARD", "EMAIL"].
func Redact(text string) (string, []string) {
	found := []string{}
	replace := func(re *regexp.Regexp, kind string, valid func(string) bool) {
		text = re.ReplaceAllStringFunc(text, func(m string) string {
			if valid != nil && !valid(m) {
				return m
			}
			if len(found) == 0 || found[len(found)-1] != kind {
				found = append(found, kind)
			}
			return "[" + kind + "]"
		})
	}
	replace(emailRe, "EMAIL", nil)
	replace(cardRe, "CARD", luhn)
	replace(ssnRe, "SSN", nil)
	replace(phoneRe, "PHONE", func(m string) bool { return digitCount(m) >= 10 })
	return text, found
}

func digitCount(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// luhn reports whether the digits in s pass the card-number checksum.
func luhn(s string) bool {
	var digits []int
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits = append(digits, int(r-'0'))
		}
	}
	sum := 0
	for i := range digits {
		d := digits[len(digits)-1-i]
		if i%2 == 1 {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return len(digits) >= 13 && sum%10 == 0
}

// ── Categories and live assist ──────────────────────────────

// GreetingCategory is matched when the agent greets the caller ("Thank
// you for calling, my name is Babu. How can I help you?"), as LCA's
// AgentGreeting category. It says nothing about the topic.
// GeneralCategory is the category of a finished call whose words matched no rule
const GeneralCategory = "GeneralEnquiry"

const GreetingCategory = "AgentGreeting"

// legacyGreeting is what the greeting category was called before; calls
// saved then may still carry it until they are brought up to date
const legacyGreeting = "HowCanIHelp"

// IsGreeting reports whether a category is the agent's greeting rather than
// a topic of the call.
func IsGreeting(category string) bool {
	return category == GreetingCategory || category == legacyGreeting
}

var greetingKeywords = []string{"how can i help", "how may i help", "how can i assist", "how may i assist",
	"what can i do for you", "how can we help", "how may we help", "thank you for calling", "thanks for calling",
	"thank you for contacting", "welcome to", "my name is", "good morning", "good afternoon", "good evening"}

type rule struct {
	category   string
	keywords   []string
	suggestion string
}

// Checked in order; a line triggers each rule's suggestion at most once per
// call. Keywords are phrases where a single word is ambiguous: "order" is
// also "in order to", "plan" also "what's the plan".
var rules = []rule{
	{"ManagerEscalation", []string{"manager", "supervisor", "escalate", "make a complaint", "file a complaint",
		"raise a complaint", "formal complaint", "speak to someone senior"},
		"Escalation requested. Acknowledge the concern and offer to bring in a supervisor."},
	{"Cancellation", []string{"cancel", "cancelled", "cancellation", "close my account", "switch provider",
		"terminate my", "end my contract"},
		"Cancellation risk. Ask what prompted it and offer a retention option before processing."},
	{"Billing", []string{"bill", "billing", "billed", "charged", "overcharged", "a charge", "the charge", "extra charge",
		"double charge", "charge my", "charge back", "chargeback", "fee", "fees", "charges", "refund", "refunded", "payment", "invoice", "my statement", "bank statement", "deducted",
		"transaction"},
		"Billing question. Review recent charges with the customer and explain any refund timeline."},
	{"TechnicalSupport", []string{"password", "log in", "login", "sign in", "error", "not working", "isn't working",
		"doesn't work", "stopped working", "broken", "reset my", "password reset", "crashing", "keeps crashing",
		"no internet", "no signal"},
		"Technical issue. Confirm the exact error and walk through the standard troubleshooting steps."},
	{"Sales", []string{"upgrade", "downgrade", "pricing", "price of", "how much does", "how much is", "new plan",
		"change my plan", "better plan", "cheaper plan", "special offer", "any offers", "want to buy", "like to buy",
		"purchase", "subscribe"},
		"Sales opportunity. Compare the relevant plans and confirm when a change would take effect."},
	// Topics without a tip of their own: the knowledge base guides the agent on these
	{"CardServices", []string{"lost my card", "lost card", "stolen", "block my card", "block the card", "card blocked",
		"card is blocked", "card has been blocked", "card was blocked", "card got blocked", "card declined",
		"card was declined", "card is declined", "card got declined", "unblock", "new card", "replacement card",
		"replace my card", "activate my card", "atm card", "atm cards", "debit card", "debit cards", "credit card", "credit cards",
		"card pin", "my pin", "card number", "card details", "last four digits"}, ""},
	{"AccountOpening", []string{"open a new", "open an account", "open a new account", "new account", "open account",
		"savings account", "current account"}, ""},
	{"Orders", []string{"my order", "an order", "the order", "order number", "orders", "delivery", "delivered",
		"parcel", "my package", "the package", "shipping", "shipment", "tracking", "exchange it", "an exchange"}, ""},
	{"PersonalLoan", []string{"personal loan", "personal loans", "loan", "loans", "emi", "foreclosure", "instalment", "installment"}, ""},
	{"InsuranceOpp", []string{"insurance", "insurance policy", "insurance policies", "life insurance", "life policy",
		"coverage", "premium", "premiums", "endowment"}, ""},
	{"AccountChanges", []string{"change my address", "update my address", "change my number", "update my number",
		"change my email", "update my details", "change my details", "change my name"}, ""},
}

const frustrationSuggestion = "Caller frustration is rising. Slow down, acknowledge the problem and apologize."

// HasTopicCategory reports whether the categories include one besides the greeting.
func HasTopicCategory(categories []string) bool {
	for _, c := range categories {
		if !IsGreeting(c) {
			return true
		}
	}
	return false
}

// FallbackCategory names a category for a finished call that matched none,
// from what the analysis found (its issues, then its topic), so the
// Categories box is never empty; "GeneralEnquiry" when nothing fits.
func FallbackCategory(issues []string, topic string) string {
	// The caller's own issues come first; the model's topic only when there are none,
	// so what the agent talked about never overrides what the caller raised
	texts := issues
	if len(issues) == 0 {
		texts = []string{topic}
	}
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		for _, c := range Categories([]store.Segment{{Speaker: SpeakerCustomer, Text: text}}) {
			if !IsGreeting(c) {
				return c
			}
		}
	}
	return GeneralCategory
}

func hasKeyword(text string, kws []string) bool {
	padded := " " + strings.Join(words(text), " ") + " "
	for _, k := range kws {
		if strings.Contains(padded, " "+k+" ") {
			return true
		}
	}
	return false
}

// Categories lists the categories the call matched, in the order they
// happened, as LCA lists them: AgentGreeting where the agent greeted the
// caller, and the topics the caller raised (mentioned earliest, then most). Topic agent lines count only when
// the caller's side wasn't captured, so the agent's scripted questions
// ("anything else about your order?") don't add topics. A call matching
// nothing has no categories.
func Categories(transcript []store.Segment) []string {
	lines := topicLines(transcript)
	type hit struct {
		category   string
		first, pos int // line of the first mention, and where in it
		mentions   int
		ruleOrder  int
	}
	var hits []hit
	for ri, r := range rules {
		h := hit{category: r.category, first: -1, ruleOrder: ri}
		for li, s := range lines {
			pos := keywordAt(s.Text, r.keywords)
			if pos < 0 {
				continue
			}
			if h.first < 0 {
				h.first, h.pos = li, pos
			}
			h.mentions++
		}
		if h.first >= 0 {
			hits = append(hits, h)
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(a.first, b.first), cmp.Compare(b.mentions, a.mentions),
			cmp.Compare(a.pos, b.pos), cmp.Compare(a.ruleOrder, b.ruleOrder))
	})
	cats := []string{}
	greetingAt, greeted := -1, false
	for _, s := range transcript {
		if s.Speaker == SpeakerAgent && keywordAt(s.Text, greetingKeywords) >= 0 {
			greetingAt, greeted = s.ID, true
			break
		}
	}
	for _, h := range hits {
		// The greeting goes in where it happened: before a topic raised later
		if greeted && greetingAt < lines[h.first].ID {
			cats = append(cats, GreetingCategory)
			greeted = false
		}
		cats = append(cats, h.category)
	}
	if greeted {
		cats = append(cats, GreetingCategory)
	}
	return cats
}

// NewCategories lists the categories in now that weren't in before, i.e.
// the ones a new line just matched.
func NewCategories(before, now []string) []string {
	var out []string
	for _, c := range now {
		if !slices.Contains(before, c) {
			out = append(out, c)
		}
	}
	return out
}

// AlertCount counts the categories that are alerts, i.e. whose name
// matches the alert pattern (LCA's "Category Alert Regular Expression").
func AlertCount(categories []string, alert *regexp.Regexp) int {
	n := 0
	for _, c := range categories {
		if alert != nil && alert.MatchString(c) {
			n++
		}
	}
	return n
}

// topicLines are the lines topics are found in: the caller's, or the
// agent's when the caller's side wasn't captured.
func topicLines(transcript []store.Segment) []store.Segment {
	var lines []store.Segment
	for _, s := range transcript {
		if s.Speaker == SpeakerCustomer {
			lines = append(lines, s)
		}
	}
	if len(lines) == 0 {
		for _, s := range transcript {
			if s.Speaker == SpeakerAgent {
				lines = append(lines, s)
			}
		}
	}
	return lines
}

// FirstMention returns the first line in which the category's topic is
// raised, e.g. where the caller first asked to cancel.
func FirstMention(transcript []store.Segment, category string) (store.Segment, bool) {
	for _, r := range rules {
		if r.category != category {
			continue
		}
		for _, s := range topicLines(transcript) {
			if keywordAt(s.Text, r.keywords) >= 0 {
				return s, true
			}
		}
	}
	return store.Segment{}, false
}

// keywordAt returns where in text the earliest of kws is said, or -1.
func keywordAt(text string, kws []string) int {
	padded := " " + strings.Join(words(text), " ") + " "
	at := -1
	for _, k := range kws {
		if i := strings.Index(padded, " "+k+" "); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	return at
}

// Suggestions returns new live-assist tips for a customer line, skipping any
// already given earlier in the call.
func Suggestions(seg store.Segment, earlier []store.Segment) []string {
	if seg.Speaker != SpeakerCustomer {
		return nil
	}
	given := map[string]bool{}
	for _, s := range earlier {
		if s.Speaker == SpeakerAssistant {
			given[s.Text] = true
		}
	}
	var out []string
	add := func(tip string) {
		if !given[tip] {
			given[tip] = true
			out = append(out, tip)
		}
	}
	if seg.SentimentScore != nil && *seg.SentimentScore <= AlertScore {
		add(frustrationSuggestion)
	}
	for _, r := range rules {
		if r.suggestion != "" && hasKeyword(seg.Text, r.keywords) {
			add(r.suggestion)
		}
	}
	return out
}

// ── Call aggregates ─────────────────────────────────────────

// ScoreTranscript returns a copy of the transcript with sentiment on every
// agent and customer line that doesn't have it yet (post-call analysis).
func ScoreTranscript(transcript []store.Segment) []store.Segment {
	out := make([]store.Segment, len(transcript))
	for i, s := range transcript {
		if (s.Speaker == SpeakerAgent || s.Speaker == SpeakerCustomer) && s.SentimentScore == nil {
			score := Score(s.Text)
			label := Label(score)
			s.SentimentScore, s.Sentiment = &score, &label
		}
		out[i] = s
	}
	return out
}

// RescoreTranscript returns a copy of the transcript with every agent and
// customer line scored again, for re-analysing a call scored by an older
// word list.
func RescoreTranscript(transcript []store.Segment) []store.Segment {
	out := make([]store.Segment, len(transcript))
	for i, s := range transcript {
		if s.Speaker == SpeakerAgent || s.Speaker == SpeakerCustomer {
			score := Score(s.Text)
			label := Label(score)
			s.SentimentScore, s.Sentiment = &score, &label
		}
		out[i] = s
	}
	return out
}

// Aggregate recomputes a call's sentiment fields from its whole transcript.
// Neutral lines count for less in the averages and the caller's label.
func Aggregate(transcript []store.Segment) store.CallUpdate {
	var caller, agent []float64
	history := []store.SentimentPoint{}
	for _, s := range transcript {
		if s.SentimentScore == nil {
			continue
		}
		switch s.Speaker {
		case SpeakerCustomer:
			caller = append(caller, *s.SentimentScore)
		case SpeakerAgent:
			agent = append(agent, *s.SentimentScore)
		default:
			continue
		}
		history = append(history, store.SentimentPoint{Time: minuteSecond(s.TimeStart), Agent: last(agent), Caller: last(caller)})
	}
	callerAvg, agentAvg := toneMean(caller), toneMean(agent)
	callerLabel, callerTrend, agentTrend := titleLabel(callerAvg), trend(caller), trend(agent)
	return store.CallUpdate{
		CallerSentiment:      &callerLabel,
		CallerSentimentTrend: &callerTrend,
		AgentSentimentTrend:  &agentTrend,
		CallerAvgSentiment:   &callerAvg,
		AgentAvgSentiment:    &agentAvg,
		SentimentHistory:     &history,
	}
}

// minuteSecond turns a "mm:ss.s" segment time into the chart's "mm:ss".
func minuteSecond(t string) string {
	mmss, _, _ := strings.Cut(t, ".")
	return mmss
}

// trend compares the last line with a clear tone against the earlier ones,
// so a closing "okay" doesn't read as the caller cheering up.
func trend(all []float64) string {
	var scores []float64
	for _, x := range all {
		if Label(x) != "neutral" {
			scores = append(scores, x)
		}
	}
	if len(scores) < 2 {
		return "Flat"
	}
	d := scores[len(scores)-1] - mean(scores[:len(scores)-1])
	switch {
	case d >= trendDelta:
		return "Up"
	case d <= -trendDelta:
		return "Down"
	default:
		return "Flat"
	}
}

// toneMean averages line scores with neutral lines counting for only
// neutralLineWeight, so "yes", "okay" and account numbers don't water down
// a frustrated caller, while one closing "thanks" doesn't make a plain call
// positive; 0 when there are no lines.
func toneMean(scores []float64) float64 {
	var sum, weight float64
	for _, x := range scores {
		w := 1.0
		if Label(x) == "neutral" {
			w = neutralLineWeight
		}
		sum += x * w
		weight += w
	}
	if weight == 0 {
		return 0
	}
	return sum / weight
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func last(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	return xs[len(xs)-1]
}

func score(s store.Segment) float64 {
	if s.SentimentScore == nil {
		return 0
	}
	return *s.SentimentScore
}
