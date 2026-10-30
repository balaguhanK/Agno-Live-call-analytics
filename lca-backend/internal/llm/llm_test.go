package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role, Content string
			} `json:"messages"`
			Format string `json:"format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "gemma2:2b" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"model not found"}`))
			return
		}
		if r.URL.Path != "/api/chat" || req.Stream || len(req.Messages) != 2 || req.Messages[0].Role != "system" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": "  echo" + req.Format + ": " + req.Messages[1].Content + " \n"}})
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "").Chat(context.Background(), "sys", "hi")
	if err != nil || got != "echo: hi" {
		t.Fatalf("got %q, %v", got, err)
	}
	// JSON mode asks Ollama for format "json"; plain Chat sends no format
	if got, err := NewClient(srv.URL, "").ChatJSON(context.Background(), "sys", "hi"); err != nil || got != "echojson: hi" {
		t.Errorf("ChatJSON got %q, %v", got, err)
	}
	if _, err := NewClient(srv.URL, "missing").Chat(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "ollama pull missing") {
		t.Errorf("missing model err = %v", err)
	}
}

// Runs against the real local model when LLM_LIVE=1 (needs Ollama + gemma2:2b)
func TestLiveGemma(t *testing.T) {
	if os.Getenv("LLM_LIVE") != "1" {
		t.Skip("set LLM_LIVE=1 to test the real model")
	}
	got, err := NewClient("", "").Chat(context.Background(), "Answer only from the passage, briefly.",
		"Passage: Duplicate charges are refunded within 3 to 5 business days.\nQuestion: How long does a refund take?")
	if err != nil || !strings.Contains(got, "3") {
		t.Fatalf("got %q, %v", got, err)
	}
}

// A short reply limit travels with the request, so the model stops sooner
func TestWithMaxTokens(t *testing.T) {
	var got float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Options map[string]float64 `json:"options"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		got = req.Options["num_predict"]
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": "{}"}})
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	if _, err := c.ChatJSON(WithMaxTokens(context.Background(), 90), "sys", "hi"); err != nil || got != 90 {
		t.Errorf("limit = %v, err %v; want 90", got, err)
	}
	if _, err := c.ChatJSON(context.Background(), "sys", "hi"); err != nil || got != maxJSONLen {
		t.Errorf("default limit = %v, err %v; want %d", got, err, maxJSONLen)
	}
}
