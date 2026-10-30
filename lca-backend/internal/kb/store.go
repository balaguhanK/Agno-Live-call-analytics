package kb

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"lca-backend/internal/embed"
)

// VectorStore keeps passages and their embeddings and finds the nearest ones.
type VectorStore interface {
	// Known maps the keys of passages already stored for model to their
	// vectors (a nil vector means the store keeps it and needn't hand it back).
	Known(ctx context.Context, model string) (map[string][]float32, error)
	// Replace makes the store hold exactly these passages. Passages from
	// Known may have a nil Vec; every other passage has one.
	Replace(ctx context.Context, model string, chunks []Chunk) error
	// Nearest returns up to n passages most similar to vec (cosine, 1 = same).
	Nearest(ctx context.Context, model string, vec []float32, n int) ([]Result, error)
	Name() string
}

const cacheFile = ".index.json"

// FileStore keeps passages in memory and caches their vectors in a JSON
// file, for running without a database.
type FileStore struct {
	path string

	mu     sync.RWMutex
	chunks []Chunk
}

func NewFileStore(dir string) *FileStore {
	return &FileStore{path: filepath.Join(dir, cacheFile)}
}

func (f *FileStore) Name() string { return "file" }

func (f *FileStore) Known(_ context.Context, _ string) (map[string][]float32, error) {
	cache := map[string][]float32{}
	if data, err := os.ReadFile(f.path); err == nil {
		_ = json.Unmarshal(data, &cache) // a corrupt cache just means re-embedding
	}
	return cache, nil
}

func (f *FileStore) Replace(_ context.Context, _ string, chunks []Chunk) error {
	// Keep only current passages so the cache doesn't grow forever
	fresh := make(map[string][]float32, len(chunks))
	for _, c := range chunks {
		fresh[c.Key] = c.Vec
	}
	if data, err := json.Marshal(fresh); err == nil {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err == nil {
			if err := os.WriteFile(f.path, data, 0o644); err != nil {
				slog.Warn("couldn't save knowledge base cache", "err", err)
			}
		}
	}
	f.mu.Lock()
	f.chunks = append([]Chunk(nil), chunks...)
	f.mu.Unlock()
	return nil
}

func (f *FileStore) Nearest(_ context.Context, _ string, vec []float32, n int) ([]Result, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	results := make([]Result, len(f.chunks))
	for i, c := range f.chunks {
		results[i] = Result{Chunk: c, Score: embed.Cosine(vec, c.Vec)}
	}
	sort.SliceStable(results, func(a, b int) bool { return results[a].Score > results[b].Score })
	return results[:min(n, len(results))], nil
}
