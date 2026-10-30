// Package embed turns text into vectors with a local embedding model served
// by Ollama (default: Google's EmbeddingGemma), for semantic search such as
// the Agent Assist knowledge base.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

const (
	DefaultURL   = "http://127.0.0.1:11434"
	DefaultModel = "embeddinggemma"
	// Embedding many chunks on CPU can take a while
	requestTimeout = 2 * time.Minute
	maxResponse    = 64 << 20
)

type Client struct {
	URL   string // Ollama base URL
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

// EmbeddingGemma is trained with these task prefixes; using them noticeably
// improves search quality.

// Query formats a search question.
func Query(text string) string { return "task: search result | query: " + text }

// Document formats a passage to be searched, with an optional title.
func Document(title, text string) string {
	if title == "" {
		title = "none"
	}
	return "title: " + title + " | text: " + text
}

// Embed returns one vector per input, in order.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{"model": c.Model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding service unreachable (is Ollama running?): %w", err)
	}
	defer res.Body.Close()
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
		Error      string      `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponse)).Decode(&out); err != nil {
		return nil, fmt.Errorf("unreadable embedding response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		if out.Error == "" {
			out.Error = res.Status
		}
		return nil, fmt.Errorf("embedding failed: %s (try: ollama pull %s)", out.Error, c.Model)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, errors.New("embedding service returned the wrong number of vectors")
	}
	return out.Embeddings, nil
}

// Cosine is the cosine similarity of two vectors: 1 means same meaning,
// around 0 means unrelated.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
