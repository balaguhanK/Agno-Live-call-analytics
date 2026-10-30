package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"lca-backend/internal/analysis"
	"lca-backend/internal/events"
	"lca-backend/internal/store"
)

// Post-call analysis of the caller, their query and how the agent spoke.
// Stats and quotes are worked out from the transcript straight away; the
// chat model then writes the insights in the background, since it takes a
// while on CPU. Without a model the rule-based insights stay.

const analysisInstructions = `You review a finished contact-centre call. Read the transcript and reply with ONLY a JSON object in exactly this shape:
{"caller": {"mood": "2 to 4 words, e.g. frustrated then relieved", "summary": "1 or 2 sentences on how the caller felt and why"},
 "query": {"issue": "what the caller wanted, in one short sentence", "outcome": "Resolved, Partly resolved or Not resolved", "detail": "one sentence on what the agent did about it", "evidence": "the exact words from the transcript that show the outcome"},
 "agent": {"tone": "2 to 4 words", "summary": "1 or 2 sentences on how the agent spoke to the caller", "strengths": ["up to 3 short points"], "improvements": ["up to 3 short points"], "rating": 1 to 5}}
Judge only from what is in the transcript; never add a number, amount, date or name that wasn't said. Words in brackets such as [CARD] are hidden personal data: never guess them.`

const (
	analysisTimeout = 3 * time.Minute
	// Points kept per list (strengths, improvements)
	maxAnalysisPoints = 3
	minAgentRating    = 1
	maxAgentRating    = 5
)

// Outcome labels shown on the dashboard
const (
	outcomeResolved    = "Resolved"
	outcomePartly      = "Partly resolved"
	outcomeNotResolved = "Not resolved"
)

// newAnalysis returns the rule-based analysis, with entities and post-call
// analytics insights, marked pending when the model will write the insights.
func (s *Server) newAnalysis(transcript []store.Segment, categories []string) store.CallAnalysis {
	a := analysis.Analyze(transcript)
	a.Entities = analysis.Entities(transcript)
	g := analysis.RuleInsights(transcript, categories)
	a.GenAI = &g
	a.Issues = analysis.RuleIssues(transcript, categories)
	a.Actions = analysis.TidyActions(analysis.RuleActions(transcript))
	a.Pending = s.writer != nil && len(spokenLines(transcript)) > 0
	return a
}

// startAnalysis has the model write the insights in the background, at most
// once at a time per call. The call must already store a pending analysis.
func (s *Server) startAnalysis(call store.Call) {
	if call.Analysis == nil || !call.Analysis.Pending {
		return
	}
	if _, running := s.analysing.LoadOrStore(call.ID, true); running {
		return
	}
	go func() {
		defer s.analysing.Delete(call.ID)
		ctx, cancel := context.WithTimeout(context.Background(), analysisTimeout)
		defer cancel()
		a := *call.Analysis
		if err := s.writeInsights(ctx, call, &a); err != nil {
			slog.Warn("post-call analysis model failed; keeping the rule-based analysis", "call", call.ID, "err", err)
		}
		if err := s.writeGenAI(ctx, call, &a); err != nil {
			slog.Warn("post-call insights model failed; keeping the rule-based insights", "call", call.ID, "err", err)
		}
		update := store.CallUpdate{Analysis: &a}
		if summary, err := s.writeSummary(ctx, call, &a); err != nil {
			slog.Warn("post-call summary model failed; keeping the rule-based summary", "call", call.ID, "err", err)
		} else {
			update.Summary = &summary
		}
		a.Pending = false
		updated, err := s.store.UpdateCall(ctx, call.ID, update)
		if err != nil {
			slog.Warn("saving post-call analysis failed", "call", call.ID, "err", err)
			return
		}
		s.publishCtx(ctx, events.Event{Type: "call", CallID: updated.ID, Agent: updated.Agent, Call: &updated})
	}()
}

// modelAnalysis is the JSON the model is asked for.
type modelAnalysis struct {
	Caller struct {
		Mood    string `json:"mood"`
		Summary string `json:"summary"`
	} `json:"caller"`
	Query struct {
		Issue    string `json:"issue"`
		Outcome  string `json:"outcome"`
		Detail   string `json:"detail"`
		Evidence string `json:"evidence"`
	} `json:"query"`
	Agent struct {
		Tone         string   `json:"tone"`
		Summary      string   `json:"summary"`
		Strengths    []string `json:"strengths"`
		Improvements []string `json:"improvements"`
		// Small models sometimes send "4" or 4.5
		Rating json.RawMessage `json:"rating"`
	} `json:"agent"`
}

// writeInsights replaces a's rule-based insights with the model's where
// they hold up against the transcript: text naming a number or reference
// nobody said is not kept, and an outcome needs a real line as evidence.
// The transcript was redacted when saved. On error a is left unchanged.
func (s *Server) writeInsights(ctx context.Context, call store.Call, a *store.CallAnalysis) error {
	reply, err := s.writer.ChatJSON(ctx, analysisInstructions, transcriptPrompt(call, 0))
	if err != nil {
		return err
	}
	var m modelAnalysis
	if err := json.Unmarshal([]byte(reply), &m); err != nil {
		return fmt.Errorf("unreadable analysis: %w", err)
	}
	caller := store.CallerInsight{Mood: clean(m.Caller.Mood), Summary: clean(m.Caller.Summary)}
	agent := store.AgentInsight{
		Tone: clean(m.Agent.Tone), Summary: clean(m.Agent.Summary),
		Strengths: points(m.Agent.Strengths), Improvements: points(m.Agent.Improvements),
		Rating: rating(m.Agent.Rating),
	}
	if caller.Summary == "" || agent.Summary == "" {
		return errors.New("analysis is missing the caller or agent summary")
	}
	tr := call.Transcript
	if grounded(tr, caller.Mood, caller.Summary) && analysis.MoodBackedUp(caller.Mood, a.Stats) {
		a.Caller = caller
	}
	if grounded(tr, append(append([]string{agent.Tone, agent.Summary}, agent.Strengths...), agent.Improvements...)...) {
		a.Agent = agent
	}
	if issue := clean(m.Query.Issue); issue != "" && grounded(tr, issue) {
		q := store.QueryInsight{Issue: issue}
		detail := analysis.FixUnfinishedClaims(clean(m.Query.Detail), tr)
		if line, ok := analysis.FindEvidence(tr, m.Query.Evidence, analysis.SpeakerAgent, analysis.SpeakerCustomer); ok && grounded(tr, detail) {
			q.Outcome, q.Detail, q.Evidence = outcome(m.Query.Outcome), detail, quoteOf(line)
		}
		a.Query = q
	}
	a.Model = s.writerModel
	return nil
}

// grounded reports whether none of texts names a number or reference that
// wasn't said on the call.
func grounded(transcript []store.Segment, texts ...string) bool {
	for _, t := range texts {
		if !analysis.Grounded(t, transcript) {
			return false
		}
	}
	return true
}

// quoteOf quotes a transcript line exactly, with its "mm:ss" time.
func quoteOf(seg store.Segment) *store.Quote {
	at, _, _ := strings.Cut(seg.TimeStart, ".")
	return &store.Quote{Time: at, Text: seg.Text}
}

// mentioned reports whether one of the words of name (4 letters or more)
// was said on the call: a product the call never mentions isn't kept.
func mentioned(transcript []store.Segment, name string) bool {
	var said strings.Builder
	for _, seg := range spokenLines(transcript) {
		said.WriteString(" " + strings.ToLower(seg.Text))
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if len(w) >= minProductWordLen && strings.Contains(said.String(), w) {
			return true
		}
	}
	return false
}

// Shorter words ("the", "app") say too little about a product
const minProductWordLen = 4

func clean(s string) string { return strings.Join(strings.Fields(plainAnswer(s)), " ") }

// points tidies a list of short points, dropping empty ones and capping the count.
func points(in []string) []string {
	var out []string
	for _, p := range in {
		if p = strings.TrimLeft(clean(p), "-•* "); p != "" && len(out) < maxAnalysisPoints {
			out = append(out, p)
		}
	}
	return out
}

// outcome maps the model's wording to one of the outcome labels, or "".
func outcome(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "part"):
		return outcomePartly
	case strings.Contains(s, "not") || strings.Contains(s, "unresolved"):
		return outcomeNotResolved
	case strings.Contains(s, "resolved"):
		return outcomeResolved
	default:
		return ""
	}
}

// rating reads a 1-5 rating sent as a number or a string; 0 if it isn't one.
func rating(raw json.RawMessage) int {
	f, err := strconv.ParseFloat(strings.Trim(string(raw), `" `), 64)
	if err != nil {
		return 0
	}
	r := int(f + 0.5)
	if r < minAgentRating || r > maxAgentRating {
		return 0
	}
	return r
}

// handleAnalyseCall serves POST /api/calls/{id}/analysis: (re)analyse an
// ended call, e.g. one from before this analysis existed.
func (s *Server) handleAnalyseCall(w http.ResponseWriter, r *http.Request) {
	call, err := s.store.GetCall(r.Context(), r.PathValue("id"))
	filter := agentFilter(current(r).user)
	if errors.Is(err, store.ErrNotFound) || (err == nil && filter != "" && call.Agent != filter) {
		writeError(w, http.StatusNotFound, "Call not found.")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	if call.Status != statusDone {
		writeError(w, http.StatusConflict, "The call is analysed when it ends.")
		return
	}
	if _, running := s.analysing.Load(call.ID); running {
		writeJSON(w, http.StatusAccepted, call)
		return
	}
	// Calls from before the current summary and issue detection get both
	// redone; the model then rewrites them if it can
	transcript, err := s.redetectInsights(r.Context(), call)
	if err != nil {
		serverError(w, r, err)
		return
	}
	// Sentiment is scored again with the current word list, and the call's
	// averages, trends and alerts recomputed from it
	transcript, err = s.rescore(r.Context(), call.ID, transcript)
	if err != nil {
		serverError(w, r, err)
		return
	}
	// Categories are found again too, so new topics apply to older calls
	categories := analysis.Categories(transcript)
	call.Categories = categories
	a := s.newAnalysis(transcript, categories)
	summary := analysis.Summarize(call, transcript, categories)
	update := analysis.Aggregate(transcript)
	alerts := analysis.AlertCount(categories, s.alert)
	update.Analysis, update.Summary, update.Categories, update.AlertCount = &a, &summary, &categories, &alerts
	updated, err := s.store.UpdateCall(r.Context(), call.ID, update)
	if err != nil {
		serverError(w, r, err)
		return
	}
	updated.Transcript = transcript
	s.startAnalysis(updated)
	writeJSON(w, http.StatusAccepted, updated)
}

// rescore scores the transcript's spoken lines again, saving the lines
// whose score changed.
func (s *Server) rescore(ctx context.Context, callID string, transcript []store.Segment) ([]store.Segment, error) {
	out := analysis.RescoreTranscript(transcript)
	for i, seg := range out {
		if seg.SentimentScore == nil {
			continue
		}
		if old := transcript[i].SentimentScore; old != nil && *old == *seg.SentimentScore {
			continue
		}
		if err := s.store.SetSegmentSentiment(ctx, callID, seg.ID, *seg.Sentiment, *seg.SentimentScore); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// redetectInsights labels the call's spoken lines again with the current
// issue / action / outcome phrases, saving the lines whose label changed.
func (s *Server) redetectInsights(ctx context.Context, call store.Call) ([]store.Segment, error) {
	out := make([]store.Segment, len(call.Transcript))
	issues := 0
	for i, seg := range call.Transcript {
		if seg.Speaker == analysis.SpeakerAgent || seg.Speaker == analysis.SpeakerCustomer {
			if insight := analysis.DetectInsight(seg.Speaker, seg.Text, issues); insight != seg.Insight {
				seg.Insight = insight
				if err := s.store.UpdateSegment(ctx, call.ID, seg); err != nil {
					return nil, err
				}
			}
			if seg.Insight == store.InsightIssue {
				issues++
			}
		}
		out[i] = seg
	}
	return out, nil
}

const genAIInstructions = `You review a finished contact-centre call for post-call analytics. Read the transcript and reply with ONLY a JSON object in exactly this shape:
{"summary": "3 or 4 sentences on who called, why, what the agent did and how the call ended",
 "topic": "the main topic in 2 to 5 words",
 "product": "the product or service discussed in 1 to 4 words, or none",
 "politeness": "yes or no: was the agent polite throughout",
 "actions": "1 or 2 sentences on what the agent did for the caller"}
Judge only from what is in the transcript; never add a number, amount, date or name that wasn't said. Words in brackets such as [CARD] are hidden personal data: never guess them.`

// writeGenAI replaces a's rule-based post-call analytics insights with the
// model's, field by field where they hold up against the transcript. On
// error a is left unchanged.
func (s *Server) writeGenAI(ctx context.Context, call store.Call, a *store.CallAnalysis) error {
	reply, err := s.writer.ChatJSON(ctx, genAIInstructions, transcriptPrompt(call, 0))
	if err != nil {
		return err
	}
	var m store.GenAIInsights
	if err := json.Unmarshal([]byte(reply), &m); err != nil {
		return fmt.Errorf("unreadable insights: %w", err)
	}
	g := store.GenAIInsights{
		Summary: clean(m.Summary), Topic: clean(m.Topic), Product: clean(m.Product),
		Politeness: strings.ToLower(clean(m.Politeness)), Actions: clean(m.Actions), Model: s.writerModel,
	}
	if g.Summary == "" || g.Topic == "" {
		return errors.New("insights are missing the summary or topic")
	}
	if g.Politeness != "yes" && g.Politeness != "no" {
		g.Politeness = ""
	}
	tr := call.Transcript
	rule := store.GenAIInsights{}
	if a.GenAI != nil {
		rule = *a.GenAI
	}
	if !grounded(tr, g.Summary) {
		g.Summary = rule.Summary
	}
	if !grounded(tr, g.Topic) {
		g.Topic = rule.Topic
	}
	if !grounded(tr, g.Actions) {
		g.Actions = rule.Actions
	}
	// Never say the agent did what they only promised
	g.Summary = analysis.FixUnfinishedClaims(g.Summary, tr)
	g.Actions = analysis.FixUnfinishedClaims(g.Actions, tr)
	if strings.EqualFold(g.Product, "none") || !mentioned(tr, g.Product) {
		g.Product = ""
	}
	a.GenAI = &g
	return nil
}

// The model fills these parts; the summary is then laid out by
// analysis.FormatSummary, so it looks the same on every call.
const postCallSummaryInstructions = `You write the record of a finished contact-centre call from its transcript.
Reply with ONLY a JSON object in exactly this shape:
{"summary": "3 or 4 sentences: who called, why, what the agent explained or did, and how the call ended",
 "topic": "the topic of the call in 2 to 5 words, e.g. life insurance",
 "actions": [{"title": "a short command for an action the agent took or agreed to take, or for something the caller asked for that still needs doing, e.g. Block the caller's card", "detail": "one sentence on it; say 'will' for anything only promised, never that it was already done", "owner": "Agent, Caller or TBD", "due": "a day or date said on the call, or TBD", "evidence": "the exact words from the transcript, the agent's or the caller's, that show this action"}],
 "issues": [{"name": "the caller's problem in 1 to 3 words, e.g. Order cancellation", "evidence": "the caller's exact words from the transcript that show it"}]}
Give 1 to 3 actions; use an empty list if the agent did nothing. Give 1 to 3 issues, the caller's main problem (the reason they called) first.
An ISSUE is a problem the caller reports: something lost, stolen, blocked, broken, not working, not received, charged wrongly, or something they are unhappy about or want to complain about or cancel.
These are NOT issues, never list them: asking for information or an explanation, asking about a product, policy, loan, rate or fee, asking how to do something, a general enquiry, saying thank you.
Examples. "I lost my credit card" is the issue "Lost credit card". "I was charged twice" is the issue "Duplicate charge". "I'm not satisfied with the reservation fee" is the issue "Reservation fee". "I want to know about the personal loan" is NOT an issue. "Could you explain your loan policy" is NOT an issue. "What is the interest rate" is NOT an issue.
If the caller reported no problem, use an empty list for issues.
Every action and issue must be backed by evidence copied word for word from the transcript; leave out anything you can't back up.
Use only what is in the transcript; never add a number, amount, date or name that wasn't said. Words in brackets such as [CARD] are hidden personal data: never guess them.`

// modelSummary is the JSON the summary prompt asks for.
type modelSummary struct {
	Summary string `json:"summary"`
	Topic   string `json:"topic"`
	Actions []struct {
		Title    string `json:"title"`
		Detail   string `json:"detail"`
		Owner    string `json:"owner"`
		Due      string `json:"due"`
		Evidence string `json:"evidence"`
	} `json:"actions"`
	Issues []struct {
		Name     string `json:"name"`
		Evidence string `json:"evidence"`
	} `json:"issues"`
}

const (
	// minSummaryChars is the shortest model summary paragraph that is kept
	minSummaryChars = 40
	// An issue name longer than this is a sentence, not a name
	maxIssueWords = 4
)

// writeSummary has the model write the call summary shown after the call,
// and puts the issues and actions it names into a. Only what holds up
// against the transcript is kept: an action or issue needs a real line as
// evidence, and text naming a number or reference nobody said is replaced
// by the transcript-based version. On error nothing changes.
func (s *Server) writeSummary(ctx context.Context, call store.Call, a *store.CallAnalysis) (string, error) {
	reply, err := s.writer.ChatJSON(ctx, postCallSummaryInstructions, transcriptPrompt(call, 0))
	if err != nil {
		return "", err
	}
	var m modelSummary
	if err := json.Unmarshal([]byte(reply), &m); err != nil {
		return "", fmt.Errorf("unreadable summary: %w", err)
	}
	tr := call.Transcript
	rule := analysis.RuleSummaryParts(call, tr, call.Categories)
	parts := analysis.SummaryParts{Summary: clean(m.Summary), Topic: clean(m.Topic)}
	if len(parts.Summary) < minSummaryChars {
		return "", errors.New("summary is missing or too short")
	}
	if !grounded(tr, parts.Summary) {
		parts.Summary = rule.Summary
	}
	// Never say the agent did what they only promised
	parts.Summary = analysis.FixUnfinishedClaims(parts.Summary, tr)
	if !grounded(tr, parts.Topic) {
		parts.Topic = rule.Topic
	}
	for _, act := range m.Actions {
		title := analysis.FixUnfinishedClaims(strings.TrimLeft(clean(act.Title), "-•* "), tr)
		// The agent's words for what was done, the caller's for what they asked for
		line, ok := analysis.FindEvidence(tr, act.Evidence, analysis.SpeakerAgent, analysis.SpeakerCustomer)
		if title == "" || !ok || !grounded(tr, title, act.Due) || len(parts.Actions) >= maxAnalysisPoints {
			continue
		}
		detail := analysis.FixUnfinishedClaims(clean(act.Detail), tr)
		if !grounded(tr, detail) {
			detail = ""
		}
		parts.Actions = append(parts.Actions, analysis.SummaryAction{
			Title: title, Detail: detail, Owner: clean(act.Owner), Due: clean(act.Due), Evidence: quoteOf(line),
		})
	}
	if len(parts.Actions) == 0 {
		// Nothing the model listed could be backed up: the agent's own lines
		parts.Actions = rule.Actions
	} else {
		// A request the caller made or a promise the agent made that the model
		// left out is added back
		for _, ra := range rule.Actions {
			if !analysis.IsFollowUp(ra) || len(parts.Actions) >= maxAnalysisPoints {
				continue
			}
			titles := make([]string, len(parts.Actions))
			for i, pa := range parts.Actions {
				titles[i] = pa.Title
			}
			if !analysis.Covered(ra.Title, titles) {
				parts.Actions = append(parts.Actions, ra)
			}
		}
	}
	a.Actions = analysis.TidyActions(parts.Actions)
	a.Issues = callIssues(tr, call.Categories, m.Issues, parts.Topic, rule.Topic, a.Issues)
	// DETAILS names every issue the caller raised, not just the one the
	// model's topic is about
	if len(a.Issues) > 1 {
		for _, issue := range a.Issues {
			if !analysis.Covered(issue, []string{parts.Topic}) {
				parts.Topic = analysis.JoinTopics(a.Issues)
				break
			}
		}
	}
	return analysis.FormatSummary(parts), nil
}

// callIssues lists every issue the caller raised, in the order they raised
// it: the model's names where a caller line backs them up, and for each
// issue line the model left out, a name from the caller's own words ("not
// satisfied with your reservation fee" → "Reservation fee"). With none, the
// model's topic, then the issues found before the model ran.
func callIssues(tr []store.Segment, categories []string, modelIssues []struct {
	Name     string `json:"name"`
	Evidence string `json:"evidence"`
}, topic, ruleTopic string, before []string) []string {
	type named struct {
		at   int
		name string
	}
	var found []named
	usedLines := map[int]bool{}
	issueLine := map[int]bool{}
	for _, l := range analysis.IssueLines(tr) {
		issueLine[l.ID] = true
	}
	for _, issue := range modelIssues {
		// Asking for information is a request, not an issue
		if analysis.IsInquiryName(issue.Name) {
			continue
		}
		line, ok := analysis.FindEvidence(tr, issue.Evidence, analysis.SpeakerCustomer)
		names := issueNames([]string{issue.Name})
		// A problem isn't raised in a thank-you: the line must be an issue
		// line, or at least not a positive one
		problem := ok && !analysis.IsInquiry(line.Text) && (issueLine[line.ID] || analysis.Score(line.Text) <= 0)
		if problem && len(names) > 0 && grounded(tr, issue.Name) && !usedLines[line.ID] {
			usedLines[line.ID] = true
			found = append(found, named{line.ID, names[0]})
		}
	}
	for _, line := range analysis.IssueLines(tr) {
		if usedLines[line.ID] {
			continue
		}
		for _, name := range analysis.IssueNamesFromLine(line.Text) {
			have := make([]string, len(found))
			for i, f := range found {
				have[i] = f.name
			}
			if !analysis.Covered(name, have) {
				found = append(found, named{line.ID, name})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].at < found[j].at })
	var out []string
	for _, f := range found {
		if len(out) < analysis.MaxIssues {
			out = append(out, f.name)
		}
	}
	// A call that only asked for information has no issue, not the model's topic
	if len(out) == 0 && topic != ruleTopic && !analysis.HasInquiry(tr) && !analysis.IsInquiryName(topic) {
		// No backed-up issue names: the model's topic ("ATM card loss") is the next best
		out = issueNames([]string{topic})
	}
	if len(out) == 0 {
		var kept []string
		for _, b := range before {
			if !analysis.IsInquiryName(b) {
				kept = append(kept, b)
			}
		}
		return kept
	}
	return out
}

// issueNames tidies the model's issue names: short, capitalised, no
// duplicates, at most analysis.MaxIssues. Sentences and quotes are dropped.
func issueNames(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, name := range in {
		name = strings.Trim(clean(name), `-•*"'“”.,;: `)
		words := strings.Fields(name)
		key := strings.ToLower(name)
		if len(words) == 0 || len(words) > maxIssueWords || seen[key] || len(out) >= analysis.MaxIssues {
			continue
		}
		seen[key] = true
		out = append(out, strings.ToUpper(name[:1])+name[1:])
	}
	return out
}
