package analysis

import (
	"strings"

	"lca-backend/internal/store"
)

// ── Transcript highlights for the post-call page ────────────

// Highlights finds, for each line labelled an issue, action or outcome, the
// sentence that makes it one ("Yes, I see here that the card has a 2%
// cashback rate. Anything else?" → "I see here that the card has a 2%
// cashback rate."). A line whose label no single sentence explains is
// highlighted whole.
//
// Every such sentence in a line is marked, so "I'll block your card right
// now. I will provide you a link." is two actions; lines from calls before a
// phrase was known are found again here.
func Highlights(transcript []store.Segment) []store.Highlight {
	var out []store.Highlight
	for _, s := range transcript {
		if s.Speaker != SpeakerAgent && s.Speaker != SpeakerCustomer {
			continue
		}
		marked := false
		for _, sentence := range sentences(s.Text) {
			kind := DetectInsight(s.Speaker, sentence, 0)
			// "And I'll have..." is filler, not a promise
			if kind == store.InsightAction && len(promiseTitles(sentence)) == 0 {
				kind = ""
			}
			if kind != "" {
				out = append(out, store.Highlight{Line: s.ID, Kind: kind, Text: trimLead(sentence, s.Speaker, kind)})
				marked = true
			}
		}
		// A label no single sentence explains: the line as a whole, unless it
		// was a promise label on filler ("And I'll have... Okay.")
		if !marked && s.Insight != "" && (s.Insight != store.InsightAction || len(promiseTitles(s.Text)) > 0) {
			out = append(out, store.Highlight{Line: s.ID, Kind: s.Insight, Text: insightSentence(s.Speaker, s.Text, s.Insight)})
		}
	}
	return out
}

// InsightPhrase is the words of a labelled line that make it an issue,
// action or outcome, without the greeting before them or the closing
// punctuation: "Hi, I'm calling about my rewards card." → "I'm calling about
// my rewards card". "" for a line without a label.
func InsightPhrase(s store.Segment) string {
	if s.Insight == "" {
		return ""
	}
	for _, h := range Highlights([]store.Segment{s}) {
		if h.Kind == s.Insight {
			return strings.TrimRight(h.Text, ".?!… ")
		}
	}
	return ""
}

// sentences splits text after each ".", "?" or "!", keeping the punctuation
// so each piece is found in the line exactly as written.
func sentences(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		if strings.ContainsRune(".?!", rune(text[i])) && (i+1 == len(text) || text[i+1] == ' ') {
			if piece := strings.TrimSpace(text[start : i+1]); piece != "" {
				out = append(out, piece)
			}
			start = i + 1
		}
	}
	if piece := strings.TrimSpace(text[start:]); piece != "" {
		out = append(out, piece)
	}
	return out
}

// insightSentence is the sentence of text that carries the label, from its
// first word to its closing punctuation.
func insightSentence(speaker, text, kind string) string {
	start := 0
	for i := 0; i <= len(text); i++ {
		if i < len(text) && !strings.ContainsRune(".?!", rune(text[i])) {
			continue
		}
		end := min(i+1, len(text))
		sentence := strings.TrimSpace(text[start:end])
		// "Yes, I see here..." starts at the words that carry the label
		if DetectInsight(speaker, sentence, 0) == kind {
			return trimLead(sentence, speaker, kind)
		}
		start = end
	}
	return strings.TrimSpace(text)
}

// trimLead drops a leading "Yes," / "Okay," / "Sure," that isn't part of
// the issue or outcome itself.
func trimLead(sentence, speaker, kind string) string {
	for {
		first, rest, ok := strings.Cut(sentence, ", ")
		if !ok || len(strings.Fields(first)) > 2 || DetectInsight(speaker, rest, 0) != kind {
			return sentence
		}
		sentence = rest
	}
}

// CategoryLines gives, for each of the call's categories, the line where
// its topic first came up, for tagging that line.
func CategoryLines(transcript []store.Segment, categories []string) map[string]int {
	out := map[string]int{}
	for _, c := range categories {
		if s, ok := FirstMention(transcript, c); ok {
			out[c] = s.ID
		}
	}
	return out
}
