package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestEmbedAgainstFakeOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/api/embed" || req.Model != "embeddinggemma" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"model \"x\" not found"}`))
			return
		}
		out := make([][]float32, len(req.Input))
		for i := range req.Input {
			out[i] = []float32{float32(i + 1), 0}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	defer srv.Close()

	vecs, err := NewClient(srv.URL, "").Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(vecs) != 2 || vecs[1][0] != 2 {
		t.Fatalf("vecs=%v err=%v", vecs, err)
	}
	_, err = NewClient(srv.URL, "missing").Embed(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "ollama pull missing") {
		t.Errorf("missing model err = %v", err)
	}
}

func TestCosineAndPrompts(t *testing.T) {
	if c := Cosine([]float32{1, 0}, []float32{2, 0}); math.Abs(c-1) > 1e-9 {
		t.Errorf("same direction = %v", c)
	}
	if c := Cosine([]float32{1, 0}, []float32{0, 3}); c != 0 {
		t.Errorf("orthogonal = %v", c)
	}
	if Cosine([]float32{1}, []float32{1, 2}) != 0 {
		t.Error("mismatched lengths should be 0")
	}
	if Query("q") != "task: search result | query: q" || Document("", "t") != "title: none | text: t" {
		t.Error("prompt formats")
	}
}

// Runs against the real local model when EMBED_LIVE=1 (needs Ollama + embeddinggemma)
func TestLiveEmbeddingGemma(t *testing.T) {
	if os.Getenv("EMBED_LIVE") != "1" {
		t.Skip("set EMBED_LIVE=1 to test the real model")
	}
	c := NewClient("", "")
	v, err := c.Embed(context.Background(), []string{
		Query("How do I get a refund for a double charge?"),
		Document("Refunds", "Duplicate charges are refunded to the card within 3 to 5 business days."),
		Document("Passwords", "Reset your password from the sign-in page."),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(v[0]) != 768 || Cosine(v[0], v[1]) <= Cosine(v[0], v[2]) {
		t.Errorf("dims=%d refund=%.3f password=%.3f", len(v[0]), Cosine(v[0], v[1]), Cosine(v[0], v[2]))
	}
}
