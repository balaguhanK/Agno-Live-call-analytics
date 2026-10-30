// Package kb is the Agent Assist knowledge base: Markdown/text documents
// split into passages, embedded with a local model (EmbeddingGemma via
// Ollama) and searched by meaning. Passages and their vectors live in a
// VectorStore (pgvector, or a file cache without a database), so a restart
// only embeds new or changed passages.
package kb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"lca-backend/internal/embed"
)

const (
	// Passages are built from whole paragraphs up to about this many characters
	targetChunkChars = 700
	embedBatch       = 16
	// Small boost for passages sharing the question's words (exact terms such
	// as product names matter even when meanings are close)
	keywordWeight = 0.08
	minKeywordLen = 4
	// Nearest passages fetched from the store before the keyword re-rank
	minCandidates   = 20
	candidateFactor = 5
)

// Chunk is one searchable passage.
type Chunk struct {
	Key     string    `json:"-"` // identifies this exact passage + model
	File    string    `json:"file"`
	Title   string    `json:"title"`   // document title
	Section string    `json:"section"` // nearest heading, if any
	Text    string    `json:"text"`
	Vec     []float32 `json:"-"`
}

// Result is a passage with its relevance (higher is better; ~0.5+ is a good match).
type Result struct {
	Chunk
	Score float64 `json:"score"`
}

type Status struct {
	Ready     bool   `json:"ready"`
	Documents int    `json:"documents"`
	Passages  int    `json:"passages"`
	Model     string `json:"model"`
	Storage   string `json:"storage,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Index struct {
	dir   string
	embed *embed.Client
	store VectorStore

	mu       sync.RWMutex
	passages int
	docs     int
	err      error
	ready    bool
}

// New indexes the documents in dir. A nil store keeps vectors in a cache
// file inside dir.
func New(dir string, client *embed.Client, store VectorStore) *Index {
	if store == nil {
		store = NewFileStore(dir)
	}
	return &Index{dir: dir, embed: client, store: store}
}

func (ix *Index) Status() Status {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	s := Status{Ready: ix.ready, Documents: ix.docs, Passages: ix.passages, Model: ix.embed.Model, Storage: ix.store.Name()}
	if ix.err != nil {
		s.Error = ix.err.Error()
	}
	return s
}

// Build (re)reads the documents and embeds any passages not in the cache.
func (ix *Index) Build(ctx context.Context) error {
	chunks, docs, err := readDocuments(ix.dir)
	if err == nil {
		err = ix.embedAll(ctx, chunks)
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.err = err
	if err != nil {
		return err
	}
	ix.passages, ix.docs, ix.ready = len(chunks), docs, true
	return nil
}

func chunkKey(model string, c Chunk) string {
	sum := sha256.Sum256([]byte(model + "\x00" + c.Title + "\x00" + c.Section + "\x00" + c.Text))
	return hex.EncodeToString(sum[:16])
}

func (ix *Index) embedAll(ctx context.Context, chunks []Chunk) error {
	model := ix.embed.Model
	for i := range chunks {
		chunks[i].Key = chunkKey(model, chunks[i])
	}
	known, err := ix.store.Known(ctx, model)
	if err != nil {
		return err
	}
	var todo []int
	for i := range chunks {
		if v, ok := known[chunks[i].Key]; ok {
			chunks[i].Vec = v // nil when the store keeps vectors itself
		} else {
			todo = append(todo, i)
		}
	}
	for start := 0; start < len(todo); start += embedBatch {
		batch := todo[start:min(start+embedBatch, len(todo))]
		inputs := make([]string, len(batch))
		for j, i := range batch {
			inputs[j] = embed.Document(heading(chunks[i]), chunks[i].Text)
		}
		vecs, err := ix.embed.Embed(ctx, inputs)
		if err != nil {
			return err
		}
		for j, i := range batch {
			chunks[i].Vec = vecs[j]
		}
	}
	if err := ix.store.Replace(ctx, model, chunks); err != nil {
		return err
	}
	slog.Info("knowledge base ready", "passages", len(chunks), "newlyEmbedded", len(todo), "model", model, "storage", ix.store.Name())
	return nil
}

func heading(c Chunk) string {
	if c.Section != "" && c.Section != c.Title {
		return c.Title + " - " + c.Section
	}
	return c.Title
}

// ErrNotReady means the index hasn't been built (or failed to build).
var ErrNotReady = errors.New("the knowledge base isn't ready yet")

// Search returns the k passages closest in meaning to the question.
func (ix *Index) Search(ctx context.Context, question string, k int) ([]Result, error) {
	ix.mu.RLock()
	ready, buildErr, passages := ix.ready, ix.err, ix.passages
	ix.mu.RUnlock()
	if !ready {
		if buildErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotReady, buildErr)
		}
		return nil, ErrNotReady
	}
	if passages == 0 {
		return nil, nil
	}
	vecs, err := ix.embed.Embed(ctx, []string{embed.Query(question)})
	if err != nil {
		return nil, err
	}
	// The store finds the nearest passages by meaning; then exact shared
	// words nudge the order. The heading counts too: it often holds the one
	// word that tells close passages apart ("I lost my ATM card" is "Lost or
	// stolen card", not "Card swallowed by an ATM")
	results, err := ix.store.Nearest(ctx, ix.embed.Model, vecs[0], max(minCandidates, k*candidateFactor))
	if err != nil {
		return nil, err
	}
	terms := keywords(question)
	for i := range results {
		results[i].Score += keywordWeight * overlap(terms, results[i].Section+" "+results[i].Text)
	}
	sort.SliceStable(results, func(a, b int) bool { return results[a].Score > results[b].Score })
	return results[:min(k, len(results))], nil
}

func keywords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) >= minKeywordLen {
			out[w] = true
		}
	}
	return out
}

// overlap is the share of the question's keywords found in text (0 to 1).
func overlap(terms map[string]bool, text string) float64 {
	if len(terms) == 0 {
		return 0
	}
	have := keywords(text)
	n := 0
	for t := range terms {
		if have[t] {
			n++
		}
	}
	return float64(n) / float64(len(terms))
}

// ── Reading and splitting documents ─────────────────────────

func readDocuments(dir string) ([]Chunk, int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil // no knowledge base yet
	}
	if err != nil {
		return nil, 0, err
	}
	var chunks []Chunk
	docs := 0
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || (ext != ".md" && ext != ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, 0, err
		}
		docs++
		chunks = append(chunks, Split(e.Name(), string(data))...)
	}
	return chunks, docs, nil
}

// Split breaks a document into passages of whole paragraphs, remembering the
// document title (first "# " heading, else the file name) and nearest heading.
func Split(file, text string) []Chunk {
	title := strings.TrimSuffix(file, filepath.Ext(file))
	section := ""
	var out []Chunk
	var buf []string
	size := 0
	flush := func() {
		if len(buf) > 0 {
			out = append(out, Chunk{File: file, Title: title, Section: section, Text: strings.Join(buf, "\n\n")})
		}
		buf, size = nil, 0
	}
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if strings.HasPrefix(para, "#") {
			line, rest, _ := strings.Cut(para, "\n")
			name := strings.TrimSpace(strings.TrimLeft(line, "#"))
			flush()
			if strings.HasPrefix(line, "# ") && len(out) == 0 && section == "" {
				title = name
			} else {
				section = name
			}
			if para = strings.TrimSpace(rest); para == "" {
				continue
			}
		}
		if size > 0 && size+len(para) > targetChunkChars {
			flush()
		}
		buf = append(buf, para)
		size += len(para)
	}
	flush()
	return out
}
