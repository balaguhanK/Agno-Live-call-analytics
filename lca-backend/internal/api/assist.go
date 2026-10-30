package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lca-backend/internal/analysis"
	"lca-backend/internal/kb"
	"lca-backend/internal/store"
)

// Agent Assist Bot: knowledge-base questions answered with RAG (retrieve
// passages with EmbeddingGemma + pgvector, then a local chat model such as
// Gemma 2 writes the answer from them), plus quick actions over the call.

// Writer turns retrieved passages into an answer (llm.Client).
type Writer interface {
	Chat(ctx context.Context, system, user string) (string, error)
	// ChatJSON is Chat with the reply constrained to a JSON object
	ChatJSON(ctx context.Context, system, user string) (string, error)
}

// ragInstructions keep the model to the passages and make it say when they
// don't answer the question.
const ragInstructions = `You help contact-centre agents during live calls.
Answer the agent's question using ONLY the numbered passages provided.
Reply in 1 to 3 short, practical sentences the agent can act on, and cite the passages you used like [1].
Write plain text only: no Markdown, no bold, no headings.
If the passages do not contain the answer, reply with exactly: NOT_FOUND`

const notFoundAnswer = "I couldn't find that in the knowledge base. Try rephrasing, or add a document about it."

const (
	maxQuestionChars = 500
	searchResults    = 3
	// Below this relevance a passage isn't trusted as an answer
	minAnswerScore = 0.35
	// Customer lines used to search for the current topic
	topicContextLines = 3
	rebuildTimeout    = 5 * time.Minute
)

type assistSource struct {
	Title   string  `json:"title"`
	Section string  `json:"section,omitempty"`
	File    string  `json:"file"`
	Score   float64 `json:"score"`
}

type assistResponse struct {
	Answer  string         `json:"answer"`
	Sources []assistSource `json:"sources,omitempty"`
	// Model that wrote the answer; empty when a passage is quoted as is
	Model string `json:"model,omitempty"`
}

func sourcesOf(results []kb.Result) []assistSource {
	out := []assistSource{}
	for _, r := range results {
		if r.Score >= minAnswerScore {
			out = append(out, assistSource{Title: r.Title, Section: r.Section, File: r.File, Score: r.Score})
		}
	}
	return out
}

// handleAssist serves POST /api/assist: {action: ask|summary|topic|email|query, question, callId}.
// "query" answers a question about the call itself from its transcript.
func (s *Server) handleAssist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action   string `json:"action"`
		Question string `json:"question"`
		CallID   string `json:"callId"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Action == "" {
		req.Action = "ask"
	}

	if req.Action == "ask" {
		if req.Question == "" || len(req.Question) > maxQuestionChars {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("question must be 1 to %d characters.", maxQuestionChars))
			return
		}
		res, err := s.askKnowledgeBase(r.Context(), req.Question)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}

	if req.Action != "summary" && req.Action != "topic" && req.Action != "email" && req.Action != "query" {
		writeError(w, http.StatusBadRequest, "action must be ask, summary, topic, email or query.")
		return
	}
	if req.Action == "query" && (req.Question == "" || len(req.Question) > maxQuestionChars) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("question must be 1 to %d characters.", maxQuestionChars))
		return
	}
	if req.CallID == "" {
		writeJSON(w, http.StatusOK, assistResponse{Answer: "Open or answer a call first; this works on the current call."})
		return
	}
	call, err := s.store.GetCall(r.Context(), req.CallID)
	filter := agentFilter(current(r).user)
	if errors.Is(err, store.ErrNotFound) || (err == nil && filter != "" && call.Agent != filter) {
		writeError(w, http.StatusNotFound, "Call not found.")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	spoken := spokenLines(call.Transcript)
	if len(spoken) == 0 {
		writeJSON(w, http.StatusOK, assistResponse{Answer: "Nothing has been said on this call yet."})
		return
	}

	// The chat model writes each one from the transcript; the rule-based
	// versions are the fallback when it is off or fails
	switch req.Action {
	case "summary":
		writeJSON(w, http.StatusOK, s.generate(r.Context(), summaryInstructions, call, plainAnswer, func() assistResponse {
			return assistResponse{Answer: plainText(analysis.Summarize(call, call.Transcript, topics(call.Transcript)))}
		}))
	case "topic":
		writeJSON(w, http.StatusOK, s.currentTopic(r.Context(), call))
	case "query":
		writeJSON(w, http.StatusOK, s.answerAboutCall(r.Context(), call, req.Question))
	case "email":
		instructions := fmt.Sprintf(emailInstructions, call.Agent)
		writeJSON(w, http.StatusOK, s.generate(r.Context(), instructions, call, tidyEmail, func() assistResponse {
			return assistResponse{Answer: followUpEmail(call)}
		}))
	}
}

const summaryInstructions = `You help a contact-centre agent during a live call. Summarise the call transcript below.
Write plain text only, no Markdown, in exactly this shape:
Reason for call: <one sentence>
- <2 to 4 short lines on what was said and done>
Next step: <one sentence, or "None yet">
Use only what is in the transcript. Words in brackets such as [CARD] are hidden personal data: never guess them.`

const topicInstructions = `You help a contact-centre agent during a live call. Read the transcript below.
Reply in plain text, no Markdown, in exactly two lines:
Topic: <the main topic in 2 to 5 words>
Customer wants: <one sentence on what the customer wants now>
Use only what is in the transcript.`

// %s is the agent's name
const emailInstructions = `You help a contact-centre agent write a follow-up email to the customer after the call below.
Write plain text only, no Markdown. Start with "Subject: " on the first line, then a blank line, then "Hello,".
Thank the customer, say briefly what was discussed and what was agreed or done, and any next steps.
Keep it under 150 words. Do not invent amounts, dates, names or reference numbers that are not in the transcript.
Words in brackets such as [CARD] are hidden personal data: leave them out.
End with:
Kind regards,
%s
Customer Support`

const (
	// The most recent part of a long call is sent, to keep the model quick
	maxPromptTranscriptChars = 6000
	// The last few lines matter most for "what's the topic now"
	topicTranscriptLines = 8
)

// transcriptPrompt formats the call's spoken lines (already redacted when
// saved) as "CUSTOMER: …", keeping the most recent maxLines (0: all) and
// at most maxPromptTranscriptChars.
func transcriptPrompt(call store.Call, maxLines int) string {
	spoken := spokenLines(call.Transcript)
	if maxLines > 0 && len(spoken) > maxLines {
		spoken = spoken[len(spoken)-maxLines:]
	}
	lines := make([]string, len(spoken))
	for i, seg := range spoken {
		lines[i] = seg.Speaker + ": " + seg.Text
	}
	text := strings.Join(lines, "\n")
	if len(text) > maxPromptTranscriptChars {
		text = text[len(text)-maxPromptTranscriptChars:]
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:] // start at a whole line
		}
	}
	return "Transcript:\n" + text
}

// generate has the chat model write from the transcript and tidies the
// result, or returns the fallback when there is no model or it fails
// (degraded mode).
func (s *Server) generate(ctx context.Context, instructions string, call store.Call, tidy func(string) string, fallback func() assistResponse) assistResponse {
	if s.writer == nil {
		return fallback()
	}
	answer, err := s.writer.Chat(ctx, instructions, transcriptPrompt(call, 0))
	if err != nil {
		slog.Warn("assist model failed; using the rule-based version", "err", err)
		return fallback()
	}
	if facts := analysis.UnsupportedFacts(answer, call.Transcript); len(facts) > 0 {
		slog.Warn("assist model named things never said on the call; using the rule-based version", "facts", facts)
		return fallback()
	}
	return assistResponse{Answer: tidy(answer), Model: s.writerModel}
}

const defaultEmailSubject = "Follow-up on your call with us"

// tidyEmail makes sure the email starts with a subject: small models
// sometimes leave it empty or skip it.
func tidyEmail(answer string) string {
	a := plainAnswer(answer)
	first, rest, _ := strings.Cut(a, "\n")
	subject, ok := strings.CutPrefix(strings.TrimSpace(first), "Subject:")
	if !ok {
		return "Subject: " + defaultEmailSubject + "\n\n" + a
	}
	if subject = strings.TrimSpace(subject); subject == "" {
		subject = defaultEmailSubject
	}
	return "Subject: " + subject + "\n" + rest
}

// tidyTopic keeps the two labelled lines asked for, adding a missing
// "Customer wants:" label and dropping doubled spaces.
func tidyTopic(answer string) string {
	lines := strings.Split(plainAnswer(answer), "\n")
	var out []string
	for _, l := range lines {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	if len(out) >= 2 && strings.HasPrefix(out[0], "Topic:") && !strings.Contains(out[1], ":") {
		out[1] = "Customer wants: " + strings.TrimPrefix(out[1], "Customer wants ")
	}
	return strings.Join(out, "\n")
}

func (s *Server) askKnowledgeBase(ctx context.Context, question string) (assistResponse, error) {
	if s.kb == nil {
		return assistResponse{}, errors.New("The knowledge base isn't set up on the server.")
	}
	results, err := s.kb.Search(ctx, question, searchResults)
	if errors.Is(err, kb.ErrNotReady) {
		s.rebuildKB()
		return assistResponse{}, errors.New("The knowledge base is still loading (is Ollama running?). Try again in a moment.")
	}
	if err != nil {
		slog.Warn("knowledge base search failed", "err", err)
		return assistResponse{}, errors.New("Knowledge base search failed. Check that Ollama is running.")
	}
	if len(results) == 0 || results[0].Score < minAnswerScore {
		return assistResponse{Answer: notFoundAnswer}, nil
	}
	sources := sourcesOf(results)
	if s.writer != nil {
		answer, err := s.writeAnswer(ctx, question, results)
		switch {
		case err == nil && strings.Contains(answer, "NOT_FOUND"):
			return assistResponse{Answer: notFoundAnswer}, nil
		case err == nil:
			return assistResponse{Answer: plainAnswer(answer), Sources: sources, Model: s.writerModel}, nil
		default:
			// Degraded mode: still useful without the model
			slog.Warn("answer model failed; quoting the best passage", "err", err)
		}
	}
	best := results[0]
	answer := best.Text
	if best.Section != "" {
		answer = best.Section + ": " + answer
	}
	return assistResponse{Answer: answer, Sources: sources}, nil
}

// writeAnswer is the generation step of RAG: the relevant passages and the
// question go to the chat model. The question is redacted first.
func (s *Server) writeAnswer(ctx context.Context, question string, results []kb.Result) (string, error) {
	clean, _ := analysis.Redact(question)
	return s.writer.Chat(ctx, ragInstructions, passagesPrompt(results, "Agent's question: "+clean))
}

// rebuildKB retries building the index in the background (e.g. Ollama
// started after the backend), at most one attempt at a time.
func (s *Server) rebuildKB() {
	if !s.kbBuilding.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.kbBuilding.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), rebuildTimeout)
		defer cancel()
		if err := s.kb.Build(ctx); err != nil {
			slog.Warn("knowledge base build failed", "err", err)
		}
	}()
}

// handleAssistStatus serves GET /api/assist/status.
func (s *Server) handleAssistStatus(w http.ResponseWriter, r *http.Request) {
	if s.kb == nil {
		writeJSON(w, http.StatusOK, kb.Status{})
		return
	}
	writeJSON(w, http.StatusOK, s.kb.Status())
}

// handleAssistReload serves POST /api/assist/reload (admins): re-read the
// documents after they change. Unchanged passages come from the cache.
func (s *Server) handleAssistReload(w http.ResponseWriter, r *http.Request) {
	if s.kb == nil {
		writeError(w, http.StatusServiceUnavailable, "The knowledge base isn't set up on the server.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rebuildTimeout)
	defer cancel()
	if err := s.kb.Build(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Couldn't rebuild the knowledge base: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.kb.Status())
}

// ── Quick actions over the current call ─────────────────────

func spokenLines(transcript []store.Segment) []store.Segment {
	var out []store.Segment
	for _, s := range transcript {
		if s.Speaker == analysis.SpeakerAgent || s.Speaker == analysis.SpeakerCustomer {
			out = append(out, s)
		}
	}
	return out
}

// topics returns the call's topic categories as readable words, e.g.
// ManagerEscalation → "manager escalation"; "general enquiry" when none.
func topics(transcript []store.Segment) []string {
	var out []string
	for _, c := range analysis.Categories(transcript) {
		if !analysis.IsGreeting(c) {
			out = append(out, readableCategory(c))
		}
	}
	if len(out) == 0 {
		out = []string{readableCategory(analysis.GreetingCategory)}
	}
	return out
}

func readableCategory(c string) string {
	if analysis.IsGreeting(c) {
		return "general enquiry"
	}
	var b strings.Builder
	for i, r := range c {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// currentTopic has the chat model name the topic from the latest lines
// (keyword categories without it) and adds the most relevant
// knowledge-base passage for what the customer said most recently.
func (s *Server) currentTopic(ctx context.Context, call store.Call) assistResponse {
	cats := topics(call.Transcript)
	var recent []string
	for i := len(call.Transcript) - 1; i >= 0 && len(recent) < topicContextLines; i-- {
		if call.Transcript[i].Speaker == analysis.SpeakerCustomer {
			recent = append([]string{call.Transcript[i].Text}, recent...)
		}
	}
	answer := "Current topic: " + strings.Join(cats, ", ") + "."
	if len(answer) > len("Current topic: ") {
		// Capitalise the first topic for display
		answer = "Current topic: " + strings.ToUpper(answer[15:16]) + answer[16:]
	}
	if len(recent) > 0 {
		answer += "\nCustomer most recently said: \"" + recent[len(recent)-1] + "\""
	}
	res := assistResponse{Answer: answer}
	if s.writer != nil {
		if generated, err := s.writer.Chat(ctx, topicInstructions, transcriptPrompt(call, topicTranscriptLines)); err != nil {
			slog.Warn("topic model failed; using keyword topics", "err", err)
		} else {
			res = assistResponse{Answer: tidyTopic(generated), Model: s.writerModel}
		}
	}
	if s.kb == nil || len(recent) == 0 {
		return res
	}
	results, err := s.kb.Search(ctx, strings.Join(recent, " "), 1)
	if err != nil || len(results) == 0 || results[0].Score < minAnswerScore {
		return res
	}
	res.Answer += "\n\nRelated guidance (" + results[0].Title + "): " + results[0].Text
	res.Sources = sourcesOf(results)
	return res
}

// followUpEmail drafts an email from the call's topics and the agent's
// closing line. Text is already redacted.
func followUpEmail(call store.Call) string {
	cats := topics(call.Transcript)
	var lastAgent string
	for _, s := range call.Transcript {
		if s.Speaker == analysis.SpeakerAgent {
			lastAgent = s.Text
		}
	}
	var b strings.Builder
	b.WriteString("Subject: Follow-up on your call with us\n\n")
	b.WriteString("Hello,\n\n")
	fmt.Fprintf(&b, "Thank you for calling us today about %s.", strings.Join(cats, " and "))
	if lastAgent != "" {
		fmt.Fprintf(&b, " As discussed: %s", lastAgent)
	}
	b.WriteString("\n\nIf you have any other questions, just reply to this email or call us again.\n\n")
	fmt.Fprintf(&b, "Kind regards,\n%s\nCustomer Support", call.Agent)
	return b.String()
}

// plainText strips the Markdown emphasis used in stored summaries.
func plainText(md string) string {
	return strings.NewReplacer("**", "", "_Generated automatically from the transcript._", "").Replace(md)
}

// kbState is embedded in Server for background rebuilds.
type kbState struct {
	kb          *kb.Index
	kbBuilding  atomic.Bool
	writer      Writer // nil: answers quote the best passage
	writerModel string
	suggestions sync.Map // call ID → *suggestTracker
}

const callQueryInstructions = `You answer a supervisor's question about the contact-centre call transcript below.
Use ONLY the transcript. Reply in 1 to 3 plain sentences, no Markdown.
If the transcript does not say, reply that the call doesn't show it.
Words in brackets such as [CARD] are hidden personal data: never guess them.`

const unsupportedQueryAnswer = "The call doesn't show that clearly enough to answer reliably."

const noModelQueryAnswer = "Questions about the call need the chat model. Set LLM_MODEL on the server and check that Ollama is running."

// answerAboutCall has the model answer a question about the call. The
// question is redacted before it reaches the model.
func (s *Server) answerAboutCall(ctx context.Context, call store.Call, question string) assistResponse {
	if s.writer == nil {
		return assistResponse{Answer: noModelQueryAnswer}
	}
	clean, _ := analysis.Redact(question)
	answer, err := s.writer.Chat(ctx, callQueryInstructions, transcriptPrompt(call, 0)+"\n\nQuestion: "+clean)
	if err != nil {
		slog.Warn("call query model failed", "err", err)
		return assistResponse{Answer: noModelQueryAnswer}
	}
	if facts := analysis.UnsupportedFacts(answer, call.Transcript); len(facts) > 0 {
		// Better no answer than a made-up number or reference
		slog.Warn("call query answer named things never said on the call", "facts", facts)
		return assistResponse{Answer: unsupportedQueryAnswer, Model: s.writerModel}
	}
	return assistResponse{Answer: analysis.FixUnfinishedClaims(plainAnswer(answer), call.Transcript), Model: s.writerModel}
}
