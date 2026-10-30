// Package llm writes text with a local chat model served by Ollama (default
// Gemma 2), e.g. the Agent Assist Bot's answers from knowledge-base passages.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultURL   = "http://127.0.0.1:11434"
	DefaultModel = "gemma2:2b"
	// Loading the model on first use can take a while on CPU
	requestTimeout = 2 * time.Minute
	// Zero: answers should stick to the call and the passages, never improvise
	temperature  = 0
	maxAnswerLen = 400 // tokens
	// JSON replies carry several fields, and one cut off can't be read
	maxJSONLen  = 800
	maxResponse = 1 << 20
	// Ollama unloads an idle model after 5 minutes by default, and loading it
	// again delays the next answer or translation; keep it ready between calls
	keepAlive = "30m"
)

type maxTokensKey struct{}

// WithMaxTokens limits how long the model's reply may be for requests made
// with ctx: a short reply is finished (and shown) sooner.
func WithMaxTokens(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, maxTokensKey{}, n)
}

type Client struct {
	URL   string
	Model string
	HTTP  *http.Client
}

func NewClient(url, model string) *Client {
	if url == "" {
		url = DefaultURL
	}
	if model == "" {
		model = DefaultModel
	}
	return &Client{URL: url, Model: model, HTTP: &http.Client{Timeout: requestTimeout}}
}

// Chat sends a system instruction and a user message and returns the reply.
func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	return c.chat(ctx, system, user, "")
}

// ChatJSON is Chat with the reply constrained to a JSON object.
func (c *Client) ChatJSON(ctx context.Context, system, user string) (string, error) {
	return c.chat(ctx, system, user, "json")
}

func (c *Client) chat(ctx context.Context, system, user, format string) (string, error) {
	payload := map[string]any{
		"model":      c.Model,
		"stream":     false,
		"keep_alive": keepAlive,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"options": map[string]any{"temperature": temperature, "num_predict": maxAnswerLen},
	}
	if format != "" {
		payload["format"] = format
		limit := maxJSONLen
		if n, ok := ctx.Value(maxTokensKey{}).(int); ok && n > 0 {
			limit = n
		}
		payload["options"] = map[string]any{"temperature": temperature, "num_predict": limit}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat model unreachable (is Ollama running?): %w", err)
	}
	defer res.Body.Close()
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponse)).Decode(&out); err != nil {
		return "", fmt.Errorf("unreadable chat response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		if out.Error == "" {
			out.Error = res.Status
		}
		return "", fmt.Errorf("chat model failed: %s (try: ollama pull %s)", out.Error, c.Model)
	}
	answer := strings.TrimSpace(out.Message.Content)
	if answer == "" {
		return "", fmt.Errorf("chat model returned an empty answer")
	}
	return answer, nil
}
