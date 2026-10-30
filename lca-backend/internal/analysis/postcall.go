package analysis

import (
	"math"
	"strings"

	"lca-backend/internal/store"
)

// ── Post-call analysis: caller, query and agent ─────────────

// The caller's start and end mood average this share of their lines (at least one)
const moodWindowShare = 1.0 / 3

// Analyze works out the call's stats and exact quotes from its scored
// transcript, with rule-based insights that a chat model may later replace.
func Analyze(transcript []store.Segment) store.CallAnalysis {
	var caller, agent []store.Segment
	callerWords, agentWords := 0, 0
	for _, s := range transcript {
		switch s.Speaker {
		case SpeakerCustomer:
			caller = append(caller, s)
			callerWords += len(words(s.Text))
		case SpeakerAgent:
			agent = append(agent, s)
			agentWords += len(words(s.Text))
		}
	}

	a := store.CallAnalysis{Stats: store.CallStats{CallerLines: len(caller), AgentLines: len(agent)}}
	st := &a.Stats
	if total := callerWords + agentWords; total > 0 {
		st.CallerTalkPct = int(math.Round(float64(callerWords) * 100 / float64(total)))
		st.AgentTalkPct = 100 - st.CallerTalkPct
	}
	st.CallerStart, st.CallerEnd = moodAt(caller, true), moodAt(caller, false)
	for _, s := range caller {
		if score(s) <= negativeScore {
			st.CallerNegativeLines++
		}
	}
	for _, s := range agent {
		switch {
		case score(s) >= positiveScore:
			st.AgentPositiveLines++
		case score(s) <= negativeScore:
			st.AgentNegativeLines++
		}
	}

	if low := extreme(caller, false); low != nil && low.Score <= negativeScore {
		a.Quotes.CallerLow = low
	}
	if best := extreme(agent, true); best != nil && best.Score >= positiveScore {
		a.Quotes.AgentBest = best
	}
	if worst := extreme(agent, false); worst != nil && worst.Score <= negativeScore {
		a.Quotes.AgentWorst = worst
	}

	a.Caller = callerInsight(st)
	if len(caller) > 0 {
		a.Query.Issue = caller[0].Text
	}
	a.Agent = agentInsight(st)
	return a
}

// moodAt labels the caller's mood over the first (or last) part of the call.
func moodAt(lines []store.Segment, start bool) string {
	if len(lines) == 0 {
		return ""
	}
	n := max(1, int(float64(len(lines))*moodWindowShare))
	part := lines[len(lines)-n:]
	if start {
		part = lines[:n]
	}
	scores := make([]float64, len(part))
	for i, s := range part {
		scores[i] = score(s)
	}
	return titleLabel(toneMean(scores))
}

// extreme returns the highest (or lowest) scored line as a quote.
func extreme(lines []store.Segment, highest bool) *store.Quote {
	var best *store.Segment
	for i := range lines {
		s := &lines[i]
		if s.SentimentScore == nil {
			continue
		}
		if best == nil || (highest && score(*s) > score(*best)) || (!highest && score(*s) < score(*best)) {
			best = s
		}
	}
	if best == nil {
		return nil
	}
	return &store.Quote{Time: minuteSecond(best.TimeStart), Text: best.Text, Score: score(*best)}
}

func callerInsight(st *store.CallStats) store.CallerInsight {
	switch {
	case st.CallerLines == 0:
		return store.CallerInsight{Mood: "Not captured", Summary: "The caller's side of the call wasn't captured."}
	case st.CallerStart == st.CallerEnd:
		return store.CallerInsight{Mood: st.CallerStart + " throughout",
			Summary: "The caller's tone stayed " + lower(st.CallerStart) + " from start to end."}
	default:
		return store.CallerInsight{Mood: st.CallerStart + " → " + st.CallerEnd,
			Summary: "The caller started " + lower(st.CallerStart) + " and ended " + lower(st.CallerEnd) + "."}
	}
}

func agentInsight(st *store.CallStats) store.AgentInsight {
	switch {
	case st.AgentLines == 0:
		return store.AgentInsight{Tone: "Not captured", Summary: "The agent's side of the call wasn't captured."}
	case st.AgentNegativeLines > 0:
		return store.AgentInsight{Tone: "Mixed", Summary: "Some of the agent's lines sounded negative; see the quote below."}
	case st.AgentPositiveLines > 0:
		return store.AgentInsight{Tone: "Positive", Summary: "The agent kept a positive, helpful tone."}
	default:
		return store.AgentInsight{Tone: "Neutral", Summary: "The agent kept a neutral, matter-of-fact tone."}
	}
}

// MoodBackedUp reports whether a model's caller mood ("frustrated then
// relieved") fits the scored transcript: a negative mood needs at least one
// negative caller line, so a calm call isn't described as frustrated.
func MoodBackedUp(mood string, st store.CallStats) bool {
	if st.CallerNegativeLines > 0 {
		return true
	}
	for _, w := range words(mood) {
		if lexicon[w] <= negativeScore {
			return false
		}
	}
	return true
}

func lower(label string) string { return strings.ToLower(label) }
