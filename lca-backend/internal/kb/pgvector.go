package kb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore keeps passages in Postgres and searches them with pgvector's
// cosine distance and an HNSW index.
type PGStore struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewPGStore prepares the kb_chunks table. The vector extension must be
// installed in the database (CREATE EXTENSION vector, as a superuser).
// log receives every pgvector operation (nil: the default logger). Only
// passage names, scores and timings are logged, never what was asked.
func NewPGStore(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) (*PGStore, error) {
	if log == nil {
		log = slog.Default()
	}
	var version string
	err := pool.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = errors.New("the pgvector extension isn't enabled in this database (run CREATE EXTENSION vector as postgres)")
	}
	if err != nil {
		log.Error("pgvector setup failed", "err", err)
		return nil, err
	}
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS kb_chunks (
			key       TEXT PRIMARY KEY,
			model     TEXT NOT NULL,
			file      TEXT NOT NULL,
			title     TEXT NOT NULL,
			section   TEXT NOT NULL,
			text      TEXT NOT NULL,
			embedding vector NOT NULL
		)`)
	if err != nil {
		log.Error("pgvector setup failed", "err", err)
		return nil, err
	}
	var rows int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM kb_chunks`).Scan(&rows)
	log.Info("pgvector connected", "version", version, "table", "kb_chunks", "rows", rows)
	return &PGStore{pool: pool, log: log}, nil
}

func (p *PGStore) Name() string { return "pgvector" }

func (p *PGStore) Known(ctx context.Context, model string) (map[string][]float32, error) {
	known, err := p.known(ctx, model)
	if err != nil {
		p.log.Error("pgvector read stored passages failed", "model", model, "err", err)
		return nil, err
	}
	p.log.Info("pgvector stored passages", "model", model, "count", len(known))
	return known, nil
}

func (p *PGStore) known(ctx context.Context, model string) (map[string][]float32, error) {
	rows, err := p.pool.Query(ctx, `SELECT key FROM kb_chunks WHERE model = $1`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[string][]float32{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		known[key] = nil
	}
	return known, rows.Err()
}

// vectorLiteral formats a vector the way pgvector reads it: [0.1,0.2,…]
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// hnswIndex makes sure an HNSW index exists for this vector size. pgvector
// needs a fixed dimension to index, so the index casts to vector(dims).
func (p *PGStore) hnswIndex(ctx context.Context, dims int) error {
	_, err := p.pool.Exec(ctx, fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS kb_chunks_hnsw_%d ON kb_chunks USING hnsw ((embedding::vector(%d)) vector_cosine_ops)`, dims, dims))
	return err
}

func (p *PGStore) Replace(ctx context.Context, model string, chunks []Chunk) error {
	start := time.Now()
	added, removed, dims, err := p.replace(ctx, model, chunks)
	if err != nil {
		p.log.Error("pgvector save failed", "model", model, "err", err)
		return err
	}
	p.log.Info("pgvector saved passages", "model", model, "total", len(chunks), "added", added,
		"kept", len(chunks)-added, "removed", removed, "dims", dims, "took", time.Since(start).Round(time.Millisecond))
	return nil
}

// replace returns how many rows were added and removed, and the vector size.
func (p *PGStore) replace(ctx context.Context, model string, chunks []Chunk) (added int, removed int64, dims int, err error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback(ctx)
	keys := make([]string, len(chunks))
	for i, c := range chunks {
		keys[i] = c.Key
		if c.Vec == nil {
			continue // already stored
		}
		dims = len(c.Vec)
		tag, err := tx.Exec(ctx, `
			INSERT INTO kb_chunks (key, model, file, title, section, text, embedding)
			VALUES ($1, $2, $3, $4, $5, $6, $7::vector)
			ON CONFLICT (key) DO NOTHING`,
			c.Key, model, c.File, c.Title, c.Section, c.Text, vectorLiteral(c.Vec))
		if err != nil {
			return 0, 0, 0, err
		}
		added += int(tag.RowsAffected())
	}
	// Remove passages that were edited or deleted, and other models' rows
	tag, err := tx.Exec(ctx, `DELETE FROM kb_chunks WHERE NOT (model = $1 AND key = ANY($2))`, model, keys)
	if err != nil {
		return 0, 0, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, 0, err
	}
	if dims > 0 {
		err = p.hnswIndex(ctx, dims)
	}
	return added, tag.RowsAffected(), dims, err
}

func (p *PGStore) Nearest(ctx context.Context, model string, vec []float32, n int) ([]Result, error) {
	start := time.Now()
	out, err := p.nearest(ctx, model, vec, n)
	if err != nil {
		p.log.Error("pgvector search failed", "model", model, "err", err)
		return nil, err
	}
	// e.g. matches="refunds-and-billing.md#Duplicate charges=0.71 | …"
	matches := make([]string, len(out))
	for i, r := range out {
		matches[i] = r.File + "#" + r.Section + "=" + strconv.FormatFloat(r.Score, 'f', 3, 64)
	}
	p.log.Info("pgvector search", "model", model, "dims", len(vec), "asked", n, "found", len(out),
		"took", time.Since(start).Round(time.Millisecond), "matches", strings.Join(matches, " | "))
	return out, nil
}

func (p *PGStore) nearest(ctx context.Context, model string, vec []float32, n int) ([]Result, error) {
	dims := len(vec)
	// The same vector(dims) cast as the index, so Postgres can use it
	rows, err := p.pool.Query(ctx, fmt.Sprintf(`
		SELECT key, file, title, section, text, 1 - (embedding::vector(%[1]d) <=> $1::vector(%[1]d)) AS score
		FROM kb_chunks
		WHERE model = $2 AND vector_dims(embedding) = %[1]d
		ORDER BY embedding::vector(%[1]d) <=> $1::vector(%[1]d)
		LIMIT $3`, dims), vectorLiteral(vec), model, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.Key, &r.File, &r.Title, &r.Section, &r.Text, &r.Score); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
