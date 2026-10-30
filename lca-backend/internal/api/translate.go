package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"lca-backend/internal/analysis"
)

// Live translation of transcript lines for the agent, written by the chat model.

const (
	maxTranslateLines = 20
	maxTranslateChars = 4000 // all lines together
)

// Languages lines can be translated into, by code
var translateLanguages = map[string]string{
	"es": "Spanish", "fr": "French", "de": "German", "it": "Italian", "pt": "Portuguese",
	"hi": "Hindi", "ta": "Tamil", "ar": "Arabic", "zh": "Chinese (Simplified)", "ja": "Japanese",
	"ko": "Korean", "nl": "Dutch", "en": "English",
}

// Lines go in keyed by number, so a line the model skips or mangles costs
// only that line, not the whole batch
const translateInstructions = `You translate lines from a contact-centre call into %[1]s.
You get a JSON object whose keys are line numbers and whose values are lines in English, e.g. {"1": "Hello", "2": "Thanks"}.
Reply with ONLY a JSON object with the same keys, each holding that line translated into %[1]s.
Translate meaning naturally; keep names, numbers and words in brackets such as [CARD] exactly as they are. Do not add notes.`

type translateResponse struct {
	// One per line, in order; "" for a line the model didn't translate
	Translations []string `json:"translations"`
	Model        string   `json:"model"`
}

// handleTranslate serves POST /api/translate: {lines: [...], language: "fr"}.
func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lines    []string `json:"lines"`
		Language string   `json:"language"`
	}
	if !decode(w, r, &req) {
		return
	}
	language, ok := translateLanguages[req.Language]
	if !ok {
		writeError(w, http.StatusBadRequest, "Unknown language.")
		return
	}
	total := 0
	for i, l := range req.Lines {
		// Redact before anything reaches the model
		req.Lines[i], _ = analysis.Redact(strings.TrimSpace(l))
		total += len(req.Lines[i])
	}
	if len(req.Lines) == 0 || len(req.Lines) > maxTranslateLines || total > maxTranslateChars {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Send 1 to %d lines, at most %d characters in all.", maxTranslateLines, maxTranslateChars))
		return
	}
	if s.writer == nil {
		writeError(w, http.StatusServiceUnavailable, "Translation needs the chat model; set LLM_MODEL on the server.")
		return
	}
	out := translateResponse{Translations: make([]string, len(req.Lines)), Model: s.writerModel}
	// Lines translated before (a repeated "Thank you", a language switched
	// back to, a page opened again) come from memory; only new ones go to the model
	var todo []int
	for i, l := range req.Lines {
		if t, ok := s.translated.get(req.Language, l); ok {
			out.Translations[i] = t
		} else {
			todo = append(todo, i)
		}
	}
	if len(todo) > 0 {
		numbered := make(map[string]string, len(todo))
		for n, i := range todo {
			numbered[strconv.Itoa(n+1)] = req.Lines[i]
		}
		prompt, _ := json.Marshal(numbered)
		reply, err := s.writer.ChatJSON(r.Context(), fmt.Sprintf(translateInstructions, language), string(prompt))
		if err != nil {
			slog.Warn("translation model failed", "err", err)
			writeError(w, http.StatusServiceUnavailable, "Translation is unavailable right now. Check that Ollama is running.")
			return
		}
		for n, t := range parseTranslations(reply, len(todo)) {
			if t != "" {
				out.Translations[todo[n]] = t
				s.translated.put(req.Language, req.Lines[todo[n]], t)
			}
		}
	}
	for _, t := range out.Translations {
		if t != "" {
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	writeError(w, http.StatusBadGateway, "The model returned an unusable translation. Try again.")
}

// maxCachedTranslations caps the translations kept in memory; when full,
// the memory starts over
const maxCachedTranslations = 5000

// translationCache remembers translated lines by language and (redacted) text.
type translationCache struct {
	mu    sync.Mutex
	lines map[string]string
}

func (c *translationCache) get(language, line string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.lines[language+"\x00"+line]
	return t, ok
}

func (c *translationCache) put(language, line, translation string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lines == nil || len(c.lines) >= maxCachedTranslations {
		c.lines = map[string]string{}
	}
	c.lines[language+"\x00"+line] = translation
}

// parseTranslations reads the model's reply as n lines in order. It takes
// the keyed object asked for, and also a {"translations": [...]} list,
// which small models sometimes send instead. Missing lines are "".
func parseTranslations(reply string, n int) []string {
	out := make([]string, n)
	var keyed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(reply), &keyed); err != nil {
		return out
	}
	if list, ok := keyed["translations"]; ok {
		var lines []string
		if json.Unmarshal(list, &lines) == nil && len(lines) == n {
			for i, l := range lines {
				out[i] = plainAnswer(l)
			}
		}
		return out
	}
	for i := range out {
		var line string
		if raw, ok := keyed[strconv.Itoa(i+1)]; ok && json.Unmarshal(raw, &line) == nil {
			out[i] = plainAnswer(line)
		}
	}
	return out
}
