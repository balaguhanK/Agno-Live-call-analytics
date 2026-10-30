package kb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"lca-backend/internal/embed"
)

// fakeOllama embeds text as [refund-ness, password-ness, outage-ness] by
// keyword, and counts how many texts it was asked to embed.
func fakeOllama(t *testing.T, embedded *atomic.Int64) *embed.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		embedded.Add(int64(len(req.Input)))
		out := make([][]float32, len(req.Input))
		for i, s := range req.Input {
			s = strings.ToLower(s)
			v := []float32{0.01, 0.01, 0.01}
			for j, kw := range []string{"refund", "password", "outage"} {
				if strings.Contains(s, kw) {
					v[j] = 1
				}
			}
			out[i] = v
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(srv.Close)
	return embed.NewClient(srv.URL, "test-model")
}

func writeDocs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"refunds.md":   "# Refund policy\n\nDuplicate charges get a refund within 3 to 5 business days.\n\n## Exceptions\n\nGift cards can't be refunded.",
		"passwords.md": "# Passwords\n\nTo reset a password, use the link on the sign-in page.",
		"outages.txt":  "Report every outage to the network team.",
		"notes.pdf":    "ignored: not Markdown or text",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSplit(t *testing.T) {
	chunks := Split("refunds.md", "# Refund policy\n\nFirst para.\n\n## Exceptions\n\nGift cards.\n\n## Contact\nCall us.")
	if len(chunks) != 3 {
		t.Fatalf("chunks = %+v", chunks)
	}
	if chunks[0].Title != "Refund policy" || chunks[0].Section != "" || chunks[0].Text != "First para." {
		t.Errorf("first = %+v", chunks[0])
	}
	if chunks[1].Section != "Exceptions" || chunks[2].Section != "Contact" || chunks[2].Text != "Call us." {
		t.Errorf("sections = %+v / %+v", chunks[1], chunks[2])
	}
	long := strings.Repeat("word ", 100) + "\n\n" + strings.Repeat("more ", 100)
	if got := Split("x.txt", long); len(got) != 2 || got[0].Title != "x" {
		t.Errorf("long text split into %d chunks, title %q", len(got), got[0].Title)
	}
}

func TestBuildSearchAndCache(t *testing.T) {
	var embedded atomic.Int64
	client := fakeOllama(t, &embedded)
	dir := writeDocs(t)

	ix := New(dir, client, nil)
	if _, err := ix.Search(context.Background(), "refund", 1); !errors.Is(err, ErrNotReady) {
		t.Errorf("search before build err = %v", err)
	}
	if err := ix.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := ix.Status()
	if !st.Ready || st.Documents != 3 || st.Passages != 4 || st.Model != "test-model" {
		t.Errorf("status = %+v", st)
	}
	first := embedded.Load()

	res, err := ix.Search(context.Background(), "How long does a refund take?", 2)
	if err != nil || len(res) != 2 {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if res[0].File != "refunds.md" || !strings.Contains(res[0].Text, "3 to 5 business days") {
		t.Errorf("top result = %+v", res[0])
	}

	// A fresh index over the same files reuses the cached embeddings
	embedded.Store(0)
	if err := New(dir, client, nil).Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if embedded.Load() != 0 {
		t.Errorf("rebuild embedded %d passages again (first build: %d)", embedded.Load(), first)
	}
	// Editing a document only re-embeds its changed passage
	_ = os.WriteFile(filepath.Join(dir, "outages.txt"), []byte("Report every outage to the network team quickly."), 0o644)
	if err := New(dir, client, nil).Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if embedded.Load() != 1 {
		t.Errorf("after one edit, embedded %d passages, want 1", embedded.Load())
	}
}

func TestMissingFolderIsEmptyNotAnError(t *testing.T) {
	var embedded atomic.Int64
	ix := New(filepath.Join(t.TempDir(), "none"), fakeOllama(t, &embedded), nil)
	if err := ix.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if res, err := ix.Search(context.Background(), "anything", 3); err != nil || len(res) != 0 {
		t.Errorf("res=%v err=%v", res, err)
	}
}

// Two passages equally close in meaning: the words in the heading decide.
// "lost" is only in the right section's heading, and the wrong one is first.
func TestSearchHeadingWordsBreakTies(t *testing.T) {
	var embedded atomic.Int64
	dir := t.TempDir()
	doc := "# Cards and ATM\n\n## Card swallowed by an ATM\n\nBlock the card and order a new card.\n\n" +
		"## Lost or stolen card\n\nBlock the card, check for fraud, and order a new card.\n"
	if err := os.WriteFile(filepath.Join(dir, "cards.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := New(dir, fakeOllama(t, &embedded), nil)
	if err := ix.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := ix.Search(context.Background(), "I lost my ATM card", 2)
	if err != nil || len(res) != 2 {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if res[0].Section != "Lost or stolen card" {
		t.Errorf("top result = %q, want %q", res[0].Section, "Lost or stolen card")
	}
}
