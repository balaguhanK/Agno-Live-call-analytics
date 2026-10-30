package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lca-backend/internal/analysis"
	"lca-backend/internal/events"
	"lca-backend/internal/kb"
	"lca-backend/internal/llm"
	"lca-backend/internal/store"
)

// Proactive Agent Assist (RAG while the call is live): when the customer says
// something the knowledge base covers ("I lost my ATM card"), guidance is
// pushed to the agent's screen without them asking, in two steps:
//  1. At once, the matching policy's essential lines: what to say and the
//     steps to take, taken straight from the passage (retrieval only, fast).
//  2. Then the chat model rewrites what to say so it fits what the customer
//     said, and the same card is updated in the transcript and the bot. The
//     steps always stay the policy's own words, in the policy's order: a
//     small model reorders and merges them.

const (
	// Stricter than typed questions (0.35): unprompted advice must be clearly
	// relevant. Customers phrase things loosely, so not much stricter: a real
	// outage complaint scored 0.48 against the outage policy
	suggestMinScore = 0.42
	// Every customer line gets guidance
	suggestMinWords = 1
	// Recent lines given to the model when no policy matched
	suggestContextLines = 5
	// Retrieval is quick; the model may need longer on CPU
	retrieveTimeout = 15 * time.Second
	suggestTimeout  = 3 * time.Minute
	// A suggested response is one or two sentences, so the model needs few tokens
	suggestMaxTokens = 90
	// Steps shown on a suggestion card
	maxSuggestSteps = 5
)

// toneRules make every suggested response read like a courteous bank officer
const toneRules = `Tone: formal, polite, warm and customer-friendly. Use full, courteous wording and no slang.
Never say the customer is frustrated, angry or upset unless they said so. Never ask for a full card number, PIN, CVV or password.
Do not write "ma'am/sir" or other alternatives; do not promise refunds, actions, timeframes or facts that were not said.`

const suggestInstructions = `You help a contact-centre agent during a live call.
The customer just said the line below. Using ONLY the numbered passages, reply with ONLY a JSON object in exactly this shape:
{"say": "ONE or TWO short sentences (at most 35 words in all) the agent can say to the customer now, answering what they said"}
` + toneRules + `
Write plain text: no Markdown and no passage numbers.
If the passages do not help with what the customer said, reply with exactly {"say": ""}.`

const suggestChatInstructions = `You help a contact-centre agent during a live call.
Reply with ONLY a JSON object in exactly this shape:
{"say": "what the agent can say next: one or two short sentences (at most 35 words in all)"}
` + toneRules + `
First acknowledge what the customer said with courtesy (for example "Thank you for informing me" or "I understand your concern"), then offer to help with that specific matter, or ask one question that relates to exactly what they said.
Use only what was said in the conversation below. Write plain text: no Markdown.
If the customer only acknowledged something, reply with a brief courteous acknowledgement.`

// policyAskWords: when the customer asks about policies, steps, rules or
// regulations, the suggestion also lists the steps to take
var policyAskWords = []string{"policy", "policies", "procedure", "process", "steps", "rule", "rules", "regulation", "regulations", "terms and conditions", "eligible", "eligibility", "entitled", "allowed", "what are the requirements", "how do i", "how can i apply", "how does it work"}

func asksForSteps(text string) bool {
	t := strings.ToLower(text)
	for _, w := range policyAskWords {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// suggestTracker lets the
// model write one suggestion at a time per call.
type suggestTracker struct {
	model sync.Mutex // the model writes one suggestion at a time; later lines wait their turn
}

// maybeSuggest looks for guidance on a customer line in the background, so
// saving the line is never slowed down. The line is already redacted.
func (s *Server) maybeSuggest(callID, agent string, line store.Segment) {
	if (s.kb == nil && s.writer == nil) || len(strings.Fields(line.Text)) < suggestMinWords {
		return
	}
	v, _ := s.suggestions.LoadOrStore(callID, &suggestTracker{})
	tr := v.(*suggestTracker)
	go s.suggestFor(tr, callID, agent, line)
}

func (s *Server) suggestFor(tr *suggestTracker, callID, agent string, line store.Segment) {
	ctx, cancel := context.WithTimeout(context.Background(), suggestTimeout)
	defer cancel()
	var results []kb.Result
	if s.kb != nil {
		searchCtx, cancelSearch := context.WithTimeout(ctx, retrieveTimeout)
		found, err := s.kb.Search(searchCtx, line.Text, searchResults)
		cancelSearch()
		if err == nil {
			for _, r := range found {
				// A file's intro text (no section heading) isn't policy
				if r.Score >= suggestMinScore && r.Section != "" {
					results = append(results, r)
				}
			}
		}
	}

	// Step 1: one line from the matching policy, or the rule-based tip, at once
	sg := &events.Suggestion{Trigger: line.Text}
	instant := false
	if len(results) > 0 {
		say, steps := essentials(results[0].Text)
		if say == "" && len(steps) > 0 {
			say = steps[0]
		}
		sg.Say = say
		// Steps and the source only when the customer asks about policies, steps or rules
		if asksForSteps(line.Text) {
			sg.Steps = steps
			for _, r := range results {
				sg.Sources = append(sg.Sources, events.Source{Title: r.Title, Section: r.Section, File: r.File, Excerpt: r.Text})
			}
		}
	} else if isAcknowledgement(line.Text) {
		sg.Say = acknowledgementReply
		instant = true
	} else if tips := analysis.Suggestions(line, nil); len(tips) > 0 {
		sg.Say = tips[0]
	} else if s.writer != nil {
		// Nothing ready-made: a neutral line shows at once, and the model's reply replaces it
		sg.Say = holdingReply
	}
	// A policy that matched is trusted as it is: its own reply is shown at once,
	// with no wait for the model (unrelated lines score far below the cut-off)
	instant = instant || (len(results) > 0 && sg.Say != "")

	useModel := s.writer != nil && !instant
	var seg store.Segment
	posted := sg.Say != ""
	if posted {
		sg.Pending = useModel
		sg.Answer = suggestionText(sg.Say, sg.Steps)
		saved, ok := s.addAssistLine(ctx, callID, line, sg)
		if !ok {
			return
		}
		seg = saved
		sg.SegmentID = seg.ID
		s.publishCtx(ctx, events.Event{Type: "assist", CallID: callID, Agent: agent, Assist: sg})
	}
	if !useModel {
		return
	}

	// Step 2: the model fits the line to what the customer said
	tr.model.Lock()
	defer tr.model.Unlock()
	say, ok := s.writeSuggestion(ctx, callID, results, line.Text)
	if !ok && !posted {
		return
	}
	final := *sg
	final.Pending = false
	if ok {
		final.Say, final.Model = say, s.writerModel
	}
	final.Answer = suggestionText(final.Say, final.Steps)
	if !posted {
		saved, ok := s.addAssistLine(ctx, callID, line, &final)
		if !ok {
			return
		}
		final.SegmentID = saved.ID
		s.publishCtx(ctx, events.Event{Type: "assist", CallID: callID, Agent: agent, Assist: &final})
		return
	}
	seg.Text = final.Answer
	// A copy: the in-memory store holds the same Assist, and readers mustn't
	// see it finished before the new text is saved
	done := *seg.Assist
	done.Say, done.Steps, done.Model, done.Pending = final.Say, final.Steps, final.Model, false
	seg.Assist = &done
	if err := s.store.UpdateSegment(ctx, callID, seg); err != nil {
		slog.Warn("updating the suggestion failed", "call", callID, "err", err)
		return
	}
	s.publishCtx(ctx, events.Event{Type: "segment", CallID: callID, Agent: agent, Segment: &seg})
	s.publishCtx(ctx, events.Event{Type: "assist", CallID: callID, Agent: agent, Assist: &final})
}

// holdingReply is shown at once for a line no policy or rule covers, until the model's reply is ready
const holdingReply = "Thank you for contacting us. I understand your concern and I would be glad to assist you with this."

// acknowledgementReply answers a plain "okay, thanks" without waiting for the model
const (
	acknowledgementReply = "You are most welcome. Please let me know if there is anything else I can assist you with."
	maxAcknowledgeWords  = 8
)

var acknowledgementRe = regexp.MustCompile(`(?i)^\s*(ok|okay|alright|all right|thanks|thank you|thank u|great|perfect|fine|got it|sure|that'?s (all|it|helpful|great|fine))\b`)

// isAcknowledgement is a short thank-you or "okay" that asks for nothing.
func isAcknowledgement(text string) bool {
	return len(strings.Fields(text)) <= maxAcknowledgeWords && !strings.Contains(text, "?") && acknowledgementRe.MatchString(text)
}

// recentLines is the last few spoken lines, for when no policy matched.
func (s *Server) recentLines(ctx context.Context, callID string) string {
	call, err := s.store.GetCall(ctx, callID)
	if err != nil {
		return ""
	}
	var lines []string
	for _, t := range call.Transcript {
		switch t.Speaker {
		case analysis.SpeakerCustomer:
			lines = append(lines, "Customer: "+t.Text)
		case analysis.SpeakerAgent:
			lines = append(lines, "Agent: "+t.Text)
		}
	}
	if len(lines) > suggestContextLines {
		lines = lines[len(lines)-suggestContextLines:]
	}
	return strings.Join(lines, "\n")
}

// writeSuggestion has the model write what the agent can say. ok is false
// when it fails or the passages don't fit what was said.
func (s *Server) writeSuggestion(ctx context.Context, callID string, results []kb.Result, said string) (string, bool) {
	ctx = llm.WithMaxTokens(ctx, suggestMaxTokens)
	// The best passage alone: a shorter prompt is answered sooner
	instructions, prompt := suggestInstructions, passagesPrompt(results[:min(len(results), 1)], "Customer said: "+said)
	if len(results) == 0 {
		instructions, prompt = suggestChatInstructions, "Conversation so far:\n"+s.recentLines(ctx, callID)+"\n\nCustomer just said: "+said
	}
	reply, err := s.writer.ChatJSON(ctx, instructions, prompt)
	if err != nil {
		slog.Warn("suggestion model failed; keeping the policy's own lines", "err", err)
		return "", false
	}
	var m struct {
		Say string `json:"say"`
	}
	if err := json.Unmarshal([]byte(reply), &m); err != nil {
		slog.Warn("unreadable suggestion; keeping the policy's own lines", "err", err)
		return "", false
	}
	say, ok := tidySuggestion(m.Say)
	if len(results) == 0 {
		// No policy involved: a short courtesy such as "You're welcome." is a fine reply
		say = strings.TrimSpace(strings.Trim(plainAnswer(m.Say), `"“”'`))
		ok = len(say) >= minChatChars
	}
	if !ok {
		return "", false // too little real advice, e.g. just "[1]"
	}
	return firstSentences(strings.TrimSpace(citeStripRe.ReplaceAllString(say, "")), maxSaySentences), true
}

// maxSaySentences keeps a suggested response compact: small models ramble
const maxSaySentences = 2

func firstSentences(text string, n int) string {
	found := sentenceRe.FindAllString(text, -1)
	if len(found) <= n {
		return text
	}
	return strings.TrimSpace(strings.Join(found[:n], ""))
}

var (
	sentenceRe = regexp.MustCompile(`[^.!?]+[.!?]+["”]?`)
	sayRe      = regexp.MustCompile(`(?i)^say:\s*`)
)

// essentials picks a passage's key lines: its "Say:" line, if it has one, and
// its first sentences as steps.
func essentials(passage string) (string, []string) {
	var say string
	var steps []string
	for _, para := range strings.Split(passage, "\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if sayRe.MatchString(para) {
			say = strings.Trim(strings.TrimSpace(sayRe.ReplaceAllString(para, "")), `"“”`)
			continue
		}
		for _, sent := range sentenceRe.FindAllString(para, -1) {
			if sent = strings.TrimSpace(sent); sent != "" && len(steps) < maxSuggestSteps {
				steps = append(steps, sent)
			}
		}
	}
	if len(steps) == 0 && say == "" {
		steps = []string{strings.TrimSpace(passage)}
	}
	return say, steps
}

// suggestionText is a suggestion as one plain-text line, for the transcript.
func suggestionText(say string, steps []string) string {
	var parts []string
	if say != "" {
		parts = append(parts, "Say: \""+say+"\"")
	}
	parts = append(parts, steps...)
	return strings.Join(parts, " ")
}

// passagesPrompt numbers the relevant passages and adds the closing line
// (the agent's question, or what the customer said).
func passagesPrompt(results []kb.Result, closing string) string {
	var b strings.Builder
	b.WriteString("Passages:\n")
	n := 0
	for _, r := range results {
		if r.Score < minAnswerScore {
			continue
		}
		n++
		b.WriteString("[" + strconv.Itoa(n) + "] ")
		b.WriteString(r.Title)
		if r.Section != "" {
			b.WriteString(" - ")
			b.WriteString(r.Section)
		}
		b.WriteString(": ")
		b.WriteString(r.Text)
		b.WriteString("\n\n")
	}
	b.WriteString(closing)
	return b.String()
}

var (
	citationRe = regexp.MustCompile(`\[\d+\]`)
	// A citation with the space before it, removed from text the agent reads
	citeStripRe       = regexp.MustCompile(`\s*\[\d+\]`)
	leadingCitationRe = regexp.MustCompile(`^\s*((?:\[\d+\]\s*)+)`)
)

// minAdviceChars is the least real text (citations aside) a suggestion needs
const minAdviceChars = 20

// minChatChars is the least a reply needs when no policy is involved
const minChatChars = 6

// tidySuggestion cleans a small model's quirks: Markdown, a citation placed
// first, wrapping quotes. ok is false when little real advice is left.
func tidySuggestion(answer string) (string, bool) {
	a := plainAnswer(answer)
	var cites string
	if m := leadingCitationRe.FindStringSubmatch(a); m != nil {
		cites = strings.Join(strings.Fields(m[1]), "")
		a = strings.TrimSpace(a[len(m[0]):])
	}
	a = strings.TrimSpace(strings.Trim(a, `"“”'`))
	if cites != "" && !strings.Contains(a, cites) {
		a += " " + cites
	}
	if len(strings.TrimSpace(citationRe.ReplaceAllString(a, ""))) < minAdviceChars {
		return "", false
	}
	return a, true
}

// plainAnswer drops any Markdown emphasis the model added; the UI shows plain text.
func plainAnswer(s string) string {
	return strings.TrimSpace(strings.NewReplacer("**", "", "__", "").Replace(s))
}

// addAssistLine shows a suggestion inline in the transcript, right after the
// customer line that prompted it, unless the call has ended meanwhile.
func (s *Server) addAssistLine(ctx context.Context, callID string, line store.Segment, sg *events.Suggestion) (store.Segment, bool) {
	lock, _ := s.callLocks.LoadOrStore(callID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	call, err := s.store.GetCall(ctx, callID)
	if err != nil || call.Status != statusInProgress {
		s.callLocks.Delete(callID) // endCall already dropped it; don't leave a new one behind
		return store.Segment{}, false
	}
	assist := &store.SegmentAssist{Query: sg.Trigger, Say: sg.Say, Steps: sg.Steps, Model: sg.Model, Pending: sg.Pending}
	for _, src := range sg.Sources {
		assist.Sources = append(assist.Sources, store.AssistSource{Title: src.Title, Section: src.Section, File: src.File, Excerpt: src.Excerpt})
	}
	saved, agent, err := s.store.AppendSegment(ctx, callID, store.Segment{
		Speaker: analysis.SpeakerAssistant, Text: sg.Answer, TimeStart: line.TimeStart, TimeEnd: line.TimeEnd, Assist: assist,
	})
	if err != nil {
		slog.Warn("saving the suggestion to the transcript failed", "call", callID, "err", err)
		return store.Segment{}, false
	}
	s.publishCtx(ctx, events.Event{Type: "segment", CallID: callID, Agent: agent, Segment: &saved})
	return saved, true
}
