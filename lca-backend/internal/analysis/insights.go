package analysis

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"lca-backend/internal/store"
)

// ── Live insights: issues, actions and outcomes ─────────────

// MaxIssues caps how many caller lines a call flags as issues, so a long
// call's Issues tab stays readable.
const MaxIssues = 3

// Phrases are matched on whole words, case-insensitively
var (
	issuePhrases = []string{
		"calling about", "calling because", "calling to", "call about", "calling regarding",
		"i have a problem", "i have an issue", "i've got a problem", "having trouble", "having a problem",
		"having an issue", "having issues", "i need help", "need some help", "i need to", "i want to",
		"i'd like to", "i would like to", "i was wondering", "can you help", "could you help",
		"not working", "doesn't work", "isn't working", "won't work", "i was charged", "i got charged",
		"can't log", "cannot log", "can't sign", "cannot sign", "i forgot my", "i lost my",
		// Problems stated without "I have a …", e.g. "I have refund issue", "there's a problem with my bill"
		"issue with", "problem with", "trouble with", "refund issue", "billing issue", "payment issue",
		"login issue", "account issue", "a complaint", "make a complaint", "want to complain",
		"charged twice", "double charged", "overcharged", "wrong amount", "extra charge",
		"want a refund", "need a refund", "get a refund", "my refund", "refund for",
		"stopped working", "is broken", "keeps crashing", "locked out", "can't access", "cannot access",
		"haven't received", "didn't receive", "never received", "not received", "still waiting",
		"want to cancel", "need to cancel", "cancel my", "question about", "asking about", "enquire about",
		"inquire about", "wondering about", "i'm calling", "i am calling",
		// Complaints: "I'm really not satisfied with your reservation fee"
		"not satisfied", "not happy", "unhappy with", "dissatisfied", "disappointed with", "upset about",
		"complain about", "complaining about", "complaint about",
		// Cards and accounts: "I have lost my card", "block my card", "open a new account"
		"lost my", "have lost", "was stolen", "got stolen", "been stolen", "block my", "block the", "unblock",
		"open a new", "open an account", "change my", "update my",
	}
	// A caller asking for information, not reporting a problem: "I want to
	// know about the personal loan"
	inquiryPhrases = []string{
		"want to know", "wanted to know", "like to know", "need to know", "know about", "know more",
		"tell me about", "tell me more", "can you tell me", "could you tell me", "explain", "information about",
		"information on", "details about", "details of", "find out", "learn about", "learn more", "interested in",
		"looking for", "enquire about", "inquire about", "enquiry", "inquiry", "question about", "asking about",
		"wondering about", "i was wondering", "what is", "what are", "what's", "how does", "how do", "how much",
		"how can i apply", "apply for", "curious", "do you offer", "do you have",
	}
	// Words that make a line a problem even when it asks something: "explain
	// why I was charged twice"
	problemWords = []string{
		"problem", "issue", "issues", "complaint", "complain", "not working", "doesn't work", "isn't working",
		"won't work", "can't", "cannot", "couldn't", "unable", "something wrong", "went wrong", "is wrong", "was wrong",
		"wrong amount", "wrong charge", "wrong number", "error", "stolen", "lost", "blocked",
		"charged", "overcharged", "not satisfied", "not happy", "unhappy", "dissatisfied", "disappointed",
		"upset", "angry", "frustrated", "broken", "locked", "never received", "not received", "haven't received",
		"didn't receive", "still waiting",
	}
	outcomePhrases = []string{
		"i've", "i have refunded", "i have processed", "i have reset", "i have updated", "i have cancelled",
		"i have sent", "i have issued", "i have applied", "i have fixed", "has been", "have been",
		"you should see", "you will see", "you'll see", "is now", "i see here", "i can see", "all set",
		"is resolved", "is fixed", "that's done", "it's done",
	}
	actionPhrases = []string{
		"i'll", "i will", "i'm going to", "i am going to", "we'll", "we will", "i can send",
		"will be sent", "follow up", "call you back", "get back to you",
	}
)

// normalize lower-cases text and joins its words with single spaces, padded,
// so phrases can be matched on word boundaries.
func normalize(text string) string {
	text = strings.NewReplacer("’", "'", "‘", "'").Replace(text)
	return " " + strings.Join(words(text), " ") + " "
}

func hasPhrase(padded string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(padded, " "+p+" ") {
			return true
		}
	}
	return false
}

// DetectInsight labels a spoken line as the caller's issue, or an agent's
// action or outcome; "" for anything else. issuesSoFar is how many earlier
// lines of the call were flagged as issues.
func DetectInsight(speaker, text string, issuesSoFar int) string {
	padded := normalize(text)
	switch speaker {
	case SpeakerCustomer:
		if issuesSoFar < MaxIssues && hasPhrase(padded, issuePhrases) && !isInquiry(padded) {
			return store.InsightIssue
		}
	case SpeakerAgent:
		// "I've refunded it and you'll see it Friday" is an outcome, not a promise
		if hasPhrase(padded, outcomePhrases) {
			return store.InsightOutcome
		}
		if hasPhrase(padded, actionPhrases) {
			return store.InsightAction
		}
	}
	return ""
}

// isInquiry is a line asking for information, with no problem in it.
func isInquiry(padded string) bool {
	return hasPhrase(padded, inquiryPhrases) && !hasPhrase(padded, problemWords)
}

// IsInquiry reports whether a caller's line asks for information without
// reporting a problem: "I want to know about the personal loan".
func IsInquiry(text string) bool { return isInquiry(normalize(text)) }

var inquiryNameRe = regexp.MustCompile(`(?i)\b(inquir\w*|enquir\w*|queries|query|information|info|question|interested|curious|explanation|details|options|personal loan inquiry)\b`)

// IsInquiryName reports whether an issue name is really a request for
// information ("Personal loan inquiry"), which isn't an issue.
func IsInquiryName(name string) bool { return inquiryNameRe.MatchString(name) }

// HasInquiry reports whether the caller asked for information and raised no issue.
func HasInquiry(transcript []store.Segment) bool {
	if len(IssueLines(transcript)) > 0 {
		return false
	}
	found := false
	for _, s := range transcript {
		if s.Speaker != SpeakerCustomer {
			continue
		}
		if hasPhrase(normalize(s.Text), problemWords) {
			return false // the caller also described a problem
		}
		found = found || IsInquiry(s.Text)
	}
	return found
}

// InquiryNames names what an inquiry-only call asked about, from its
// categories: "Personal loan inquiry".
func InquiryNames(categories []string) []string {
	if labels := IssueLabels(categories); len(labels) > 0 {
		return []string{labels[0] + " inquiry"}
	}
	return []string{"General inquiry"}
}

// CountInsights counts transcript lines with the given insight label.
func CountInsights(transcript []store.Segment, insight string) int {
	n := 0
	for _, s := range transcript {
		if s.Insight == insight {
			n++
		}
	}
	return n
}

// InsightLines returns the text of the lines with the given insight label.
func InsightLines(transcript []store.Segment, insight string) []string {
	out := []string{}
	for _, s := range transcript {
		if s.Insight == insight {
			out = append(out, s.Text)
		}
	}
	return out
}

// ── Entities ────────────────────────────────────────────────

// maxEntities caps each entity type's list
const maxEntities = 10

const numWord = `(?:\d+(?:[.,]\d+)?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|fifteen|twenty|thirty|fifty|hundred)`

var (
	orgRe = regexp.MustCompile(`\b((?:[A-Z][A-Za-z&]+\s){0,2}(?:Bank|Insurance|Inc|Ltd|Corp|Corporation|Company|Telecom|Airlines|Energy|Mobile|Group))\b|\b([A-Za-z][\w-]*\.(?:com|org|net|io|co))\b`)
	// Amounts with a currency before ("$45", "₹500", "Rs 250") or after
	// ("500 rupees", "2 lakhs"), and counts and periods ("3 items", "5 days")
	qtyRe = regexp.MustCompile(`(?i)(?:\$|₹|€|£|\brs\.?|\binr|\busd)\s?\d[\d,]*(?:\.\d+)?|\b` + numWord + `(?:\s(?:to|or)\s` + numWord + `)?\s?(?:%|percent\b|dollars?\b|bucks\b|cents?\b|pounds?\b|euros?\b|rupees?\b|rs\b|inr\b|paise\b|lakhs?\b|crores?\b|thousand\b|business days?\b|working days?\b|days?\b|hours?\b|minutes?\b|weeks?\b|months?\b|years?\b|items?\b|pieces?\b|units?\b|times\b|gb\b|mb\b|kg\b|km\b)`)
	// "order number is 4521", "ticket #A-77"; and codes mixing letters and
	// digits such as QX123, wherever they are said
	refAfterRe = regexp.MustCompile(`(?i)\b(?:order|reference|ref|ticket|case|account|booking|confirmation|tracking|invoice|policy)\s*(?:number|no\.?|id|#)?\s*(?:is\s+)?#?\s*([A-Za-z0-9][A-Za-z0-9-]{2,})\b`)
	codeRe     = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9-]{3,}\b`)
	// A number read out after its name, as callers say them: "my order
	// number is four five two one", "order ID A B 1 2 3". Letters must be
	// capitals (how speech-to-text writes spelled letters) and not "I"
	spokenRefRe = regexp.MustCompile(`(?i:\b(?:order|reference|ref|ticket|case|account|booking|confirmation|tracking|invoice|policy|transaction|complaint)\s*(?:number|no\.?|id|#)?\s*(?:is\s+|was\s+|:\s*)?)((?:(?:[A-HJ-Z]|\d+|(?i:zero|oh|one|two|three|four|five|six|seven|eight|nine))[\s,-]*){3,})`)
	// Digits read out on their own, as callers say the last digits of a card or
	// a code: "nine eight four two"
	spokenDigitsRe = regexp.MustCompile(`(?i)\b(?:(?:zero|one|two|three|four|five|six|seven|eight|nine)[\s,-]+){2,}(?:zero|one|two|three|four|five|six|seven|eight|nine)\b`)
	// Numbers no other entity covers: "That's 10", "order 4521"
	bareNumberRe = regexp.MustCompile(`\b\d[\d,]*(?:\.\d+)?\b`)
	// Products and services a call is about, as said: the longer names first,
	// so "rewards card" is one product, not "card"
	productRe = regexp.MustCompile(`(?i)\b(?:(?:credit|debit|atm|rewards?|reward|sim|gift|prepaid|loyalty|membership|id|new|replacement)\s+cards?|(?:savings|current|checking|bank|online|mobile|joint|salary|business|new)\s+accounts?|(?:life|health|car|motor|home|travel|term|universal life|whole life)\s+insurance|insurance\s+polic(?:y|ies)|(?:home|car|personal|student|business|gold)\s+loans?|mortgages?|fixed deposits?|credit limit|(?:internet|broadband|wifi|data|mobile|phone|prepaid|postpaid)\s+(?:plans?|bills?|connection|service|pack)|broadband|internet|subscriptions?|net ?banking|mobile app|the app|cashback|reward points|cards?|accounts?|orders?|polic(?:y|ies)|loans?|plans?|bills?|refunds?|reservations?|bookings?|deliver(?:y|ies)|parcels?|packages?|passwords?)\b`)
	dateRe    = regexp.MustCompile(`(?i)\b(?:(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?\s\d{1,2}(?:st|nd|rd|th)?(?:,?\s\d{4})?|\d{1,2}(?:st|nd|rd|th)?\s+of\s+(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*(?:,?\s\d{4})?|(?:on|by|before|after|until|from)\s+the\s+\d{1,2}(?:st|nd|rd|th)|\d{1,2}[/.-]\d{1,2}[/.-]\d{2,4}|\d{1,2}/\d{1,2}|(?:19|20)\d{2}|\d{1,2}(?::\d{2})?\s?(?:am|pm)|\d{1,2}:\d{2}|(?:next|this|last)\s+(?:monday|tuesday|wednesday|thursday|friday|saturday|sunday|week|month|year|weekend)|monday|tuesday|wednesday|thursday|friday|saturday|sunday|today|tonight|tomorrow|yesterday|end of (?:the )?(?:week|month))\b`)
)

// Capitalised words that follow "thanks," or "hi," but aren't names
var notNames = map[string]bool{
	"Yes": true, "No": true, "Okay": true, "Ok": true, "Sure": true, "So": true, "And": true, "But": true,
	"The": true, "That": true, "This": true, "Thanks": true, "Thank": true, "Great": true, "Perfect": true,
	"Good": true, "Well": true, "Please": true, "Sir": true, "Madam": true, "Again": true, "Everyone": true,
	"There": true, "How": true, "What": true, "Have": true, "Bye": true, "Goodbye": true, "Very": true,
	"Sorry": true, "Here": true, "Calling": true, "Not": true, "Just": true, "Also": true, "Really": true, "All": true,
	"Monday": true, "Tuesday": true, "Wednesday": true, "Thursday": true, "Friday": true, "Saturday": true, "Sunday": true,
	"Customer": true, "Support": true, "Team": true, "Everybody": true, "Folks": true,
}

// Entities lists the people, organisations, quantities and dates mentioned
// on the call. Redacted placeholders such as [NAME] are never returned.
func Entities(transcript []store.Segment) map[string][]string {
	entities := map[string][]string{}
	seen := map[string]bool{}
	add := func(kind, value string) {
		value = strings.TrimSpace(strings.TrimRightFunc(value, func(r rune) bool { return unicode.IsPunct(r) && r != '%' }))
		key := kind + "|" + strings.ToLower(value)
		if value == "" || seen[key] || len(entities[kind]) >= maxEntities || strings.ContainsAny(value, "[]") {
			return
		}
		seen[key] = true
		entities[kind] = append(entities[kind], value)
	}
	for _, s := range transcript {
		if s.Speaker != SpeakerAgent && s.Speaker != SpeakerCustomer {
			continue
		}
		// Where amounts, dates and references were found, so the numbers
		// inside them aren't listed again on their own; they are added in the
		// order they were said
		var taken [][]int
		type hit struct {
			at          int
			kind, value string
		}
		var hits []hit
		found := func(kind string, loc []int, value string) {
			taken = append(taken, loc)
			hits = append(hits, hit{loc[0], kind, value})
		}
		mark := func(kind string, re *regexp.Regexp) {
			for _, loc := range re.FindAllStringIndex(s.Text, -1) {
				found(kind, loc, s.Text[loc[0]:loc[1]])
			}
		}
		for _, m := range orgRe.FindAllStringSubmatch(s.Text, -1) {
			add(store.EntityOrganization, trimOrgLead(m[1])+m[2])
		}
		mark(store.EntityDate, dateRe)
		mark(store.EntityQuantity, qtyRe)
		for _, loc := range productRe.FindAllStringIndex(s.Text, -1) {
			// "in order to" isn't an order
			if strings.EqualFold(s.Text[loc[0]:loc[1]], "order") && strings.HasSuffix(strings.ToLower(s.Text[:loc[0]]), "in ") {
				continue
			}
			found(store.EntityProduct, loc, strings.ToLower(s.Text[loc[0]:loc[1]]))
		}
		for _, m := range spokenRefRe.FindAllStringSubmatchIndex(s.Text, -1) {
			// Only references read out in pieces; one written as a single code
			// ("ZX-4471") is found as it is below
			spoken := strings.TrimRight(s.Text[m[2]:m[3]], " ,-")
			if !strings.ContainsAny(spoken, " ,") {
				continue
			}
			if ref := spokenReference(spoken); len(ref) >= minReferenceLen && hasDigit(ref) {
				found(store.EntityReference, m[2:4], ref)
			}
		}
		for _, loc := range spokenDigitsRe.FindAllStringIndex(s.Text, -1) {
			// Short runs only: the last digits of a card, not a whole card or phone number
			if ref := spokenReference(s.Text[loc[0]:loc[1]]); !inside(loc, taken) && len(ref) <= maxSpokenDigits {
				found(store.EntityReference, loc, ref)
			}
		}
		for _, m := range refAfterRe.FindAllStringSubmatchIndex(s.Text, -1) {
			if ref := s.Text[m[2]:m[3]]; hasDigit(ref) && !inside(m[2:4], taken) {
				found(store.EntityReference, m[2:4], strings.ToUpper(ref))
			}
		}
		for _, loc := range codeRe.FindAllStringIndex(s.Text, -1) {
			if code := s.Text[loc[0]:loc[1]]; isCode(code) && !inside(loc, taken) {
				found(store.EntityReference, loc, strings.ToUpper(code))
			}
		}
		for _, loc := range bareNumberRe.FindAllStringIndex(s.Text, -1) {
			if !inside(loc, taken) {
				hits = append(hits, hit{loc[0], store.EntityQuantity, s.Text[loc[0]:loc[1]]})
			}
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
		for _, h := range hits {
			add(h.kind, h.value)
		}
	}
	if people := People(transcript, ""); len(people) > 0 {
		entities[store.EntityPerson] = people
	}
	return entities
}

// Role labels on PERSON entities: "Bob Smith (Agent)"
const (
	RoleAgent    = "Agent"
	RoleCustomer = "Customer"
)

var (
	// Someone giving their own name: "my name is Ravi", "this is Bob", "I'm Priya"
	selfNameRe = regexp.MustCompile(`(?:[Mm]y name is|[Mm]y name's|[Tt]his is|[Ii] am|[Ii]'m)\s+([A-Z][a-z]{1,20}(?:\s+[A-Z][a-z]{1,20})?)\b`)
	// Someone addressing the other person by name: "Hi Bob", "thank you, Ravi", "Mr Kumar"
	addressNameRe = regexp.MustCompile(`(?:[Hh]i|[Hh]ello|[Hh]ey|[Tt]hanks|[Tt]hank you|[Dd]ear|[Ss]peaking with|Mr\.?|Mrs\.?|Ms\.?|Miss|[Gg]ood (?:morning|afternoon|evening))[,\s]+([A-Z][a-z]{1,20})\b`)
)

// People names the agent and the customer: the agent by accountName (their
// name on the dashboard, e.g. "Bob Smith") when known, else by the name they
// gave on the call; the customer by the name they gave, or the name the
// agent called them. Each is labelled with their role, agent first. A name
// nobody said is never guessed.
func People(transcript []store.Segment, accountName string) []string {
	var agents, customers []string
	addTo := func(list *[]string, name string) {
		first, _, _ := strings.Cut(name, " ")
		if notNames[first] {
			return
		}
		for _, n := range *list {
			if strings.EqualFold(n, name) {
				return
			}
		}
		*list = append(*list, name)
	}
	for _, s := range transcript {
		self, other := &agents, &customers
		switch s.Speaker {
		case SpeakerAgent:
		case SpeakerCustomer:
			self, other = &customers, &agents
		default:
			continue
		}
		for _, m := range selfNameRe.FindAllStringSubmatch(s.Text, -1) {
			addTo(self, strings.TrimSpace(m[1]))
		}
		for _, m := range addressNameRe.FindAllStringSubmatch(s.Text, -1) {
			addTo(other, m[1])
		}
	}
	var out []string
	if accountName != "" {
		out = append(out, accountName+" ("+RoleAgent+")")
		// "this is Bob" on the call is the same agent as "Bob Smith"
		accountFirst, _, _ := strings.Cut(strings.ToLower(accountName), " ")
		kept := agents[:0]
		for _, n := range agents {
			first, _, _ := strings.Cut(strings.ToLower(n), " ")
			if first != accountFirst {
				kept = append(kept, n)
			}
		}
		agents = kept
	}
	for _, n := range agents {
		out = append(out, n+" ("+RoleAgent+")")
	}
	for _, n := range customers {
		// A name already given to the agent isn't the customer's too
		if !slices.ContainsFunc(out, func(o string) bool { return strings.HasPrefix(strings.ToLower(o), strings.ToLower(n)+" ") }) {
			out = append(out, n+" ("+RoleCustomer+")")
		}
	}
	return out
}

// minReferenceLen is the fewest characters a read-out reference has: fewer
// is a count ("order of 3"), not a number
const minReferenceLen = 3

// maxSpokenDigits is the most digits read out on their own that are listed as an entity;
// a longer run is a whole card or phone number, which isn't echoed
const maxSpokenDigits = 6

var digitWords = map[string]string{
	"zero": "0", "oh": "0", "one": "1", "two": "2", "three": "3", "four": "4",
	"five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9",
}

// spokenReference joins a reference read out piece by piece: "four five two
// one" → "4521", "A B 1 2 3" → "AB123".
func spokenReference(spoken string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(spoken, func(r rune) bool { return r == ' ' || r == ',' || r == '-' }) {
		if d, ok := digitWords[strings.ToLower(part)]; ok {
			b.WriteString(d)
		} else {
			b.WriteString(strings.ToUpper(part))
		}
	}
	return b.String()
}

// inside reports whether loc lies within one of the spans.
func inside(loc []int, spans [][]int) bool {
	for _, sp := range spans {
		if loc[0] >= sp[0] && loc[1] <= sp[1] {
			return true
		}
	}
	return false
}

// ── Post-call insights without a model ──────────────────────

// maxInsightLines caps the agent lines quoted as the call's actions
const maxInsightLines = 3

// RuleInsights works out the post-call analytics insights from the scored
// transcript, for when no chat model writes them.
func RuleInsights(transcript []store.Segment, categories []string) store.GenAIInsights {
	a := Analyze(transcript)
	g := store.GenAIInsights{Politeness: "yes"}
	if a.Stats.AgentNegativeLines > 0 {
		g.Politeness = "no"
	}
	issues := InsightLines(transcript, store.InsightIssue)
	switch {
	case len(issues) > 0:
		g.Topic = issues[0]
	case len(categories) > 0:
		if labels := IssueLabels(categories); len(labels) > 0 {
			g.Topic = labels[0]
		}
	}
	done := InsightLines(transcript, store.InsightOutcome)
	done = append(done, InsightLines(transcript, store.InsightAction)...)
	if len(done) > maxInsightLines {
		done = done[:maxInsightLines]
	}
	g.Actions = strings.Join(done, " ")
	if g.Actions == "" {
		g.Actions = "No agent actions detected."
	}
	g.Summary = a.Caller.Summary
	if a.Query.Issue != "" {
		g.Summary = "The caller said: \"" + a.Query.Issue + "\" " + g.Summary
	}
	return g
}

func hasDigit(s string) bool { return strings.ContainsAny(s, "0123456789") }

// isCode tells a reference code such as "QX123" or "A-4471" from an ordinary
// word or number: it mixes letters and digits, and isn't an amount or time
// like "5pm", "2nd" or "10GB".
func isCode(s string) bool {
	letters, digits := 0, 0
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsDigit(r):
			digits++
		}
	}
	if letters == 0 || digits < 2 {
		return false
	}
	lower := strings.ToLower(s)
	for _, unit := range []string{"am", "pm", "st", "nd", "rd", "th", "gb", "mb", "kb", "km", "kg", "s", "k", "m"} {
		if strings.HasSuffix(lower, unit) && !strings.ContainsFunc(strings.TrimSuffix(lower, unit), unicode.IsLetter) {
			return false
		}
	}
	return true
}

// splitCamel turns "ManagerEscalation" into "Manager escalation".
func splitCamel(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte(' ')
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// orgLeadWords are capitalised words that open a sentence, not a company name:
// "Calling Acme Bank" is the organisation "Acme Bank"
var orgLeadWords = map[string]bool{
	"calling": true, "welcome": true, "thanks": true, "thank": true, "hello": true, "hi": true,
	"good": true, "this": true, "you": true, "from": true, "at": true, "to": true, "the": true,
}

func trimOrgLead(name string) string {
	for {
		first, rest, found := strings.Cut(name, " ")
		if !found || !orgLeadWords[strings.ToLower(first)] {
			return name
		}
		name = rest
	}
}
