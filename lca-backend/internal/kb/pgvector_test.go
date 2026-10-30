package kb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Needs TEST_DATABASE_URL with pgvector enabled; works in a throwaway schema.
func TestPGStore(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to test pgvector")
	}
	ctx := context.Background()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "lca_test_" + hex.EncodeToString(b)
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var logs bytes.Buffer
	st, err := NewPGStore(ctx, pool, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	chunks := []Chunk{
		{Key: "k1", File: "a.md", Title: "Refunds", Section: "Duplicates", Text: "refund text", Vec: []float32{1, 0, 0}},
		{Key: "k2", File: "b.md", Title: "Passwords", Text: "password text", Vec: []float32{0, 1, 0}},
	}
	if err := st.Replace(ctx, "m", chunks); err != nil {
		t.Fatal(err)
	}
	known, _ := st.Known(ctx, "m")
	if len(known) != 2 {
		t.Errorf("known = %v", known)
	}
	res, err := st.Nearest(ctx, "m", []float32{0.9, 0.1, 0}, 2)
	if err != nil || len(res) != 2 || res[0].Key != "k1" || res[0].Section != "Duplicates" || res[0].Score < 0.9 || res[1].Key != "k2" {
		t.Fatalf("nearest = %+v, %v", res, err)
	}
	for _, want := range []string{"pgvector connected", "added=2", "pgvector search", "a.md#Duplicates=0.99"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q:\n%s", want, logs.String())
		}
	}

	// k2 removed, k1 kept without re-sending its vector, k3 added
	if err := st.Replace(ctx, "m", []Chunk{{Key: "k1"}, {Key: "k3", File: "c.md", Title: "Outages", Text: "x", Vec: []float32{0, 0, 1}}}); err != nil {
		t.Fatal(err)
	}
	known, _ = st.Known(ctx, "m")
	if _, ok := known["k2"]; ok || len(known) != 2 {
		t.Errorf("after replace known = %v", known)
	}
	var indexes int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = $1 AND indexname = 'kb_chunks_hnsw_3'`, schema).Scan(&indexes)
	if indexes != 1 {
		t.Errorf("HNSW index missing")
	}
	// A different model's vectors replace this one's
	if err := st.Replace(ctx, "other", []Chunk{{Key: "z", File: "z.md", Title: "Z", Text: "z", Vec: []float32{1, 1, 0}}}); err != nil {
		t.Fatal(err)
	}
	if k, _ := st.Known(ctx, "m"); len(k) != 0 {
		t.Errorf("old model rows left: %v", k)
	}
}

func TestVectorLiteral(t *testing.T) {
	if got := vectorLiteral([]float32{1, -0.5, 0.25}); got != "[1,-0.5,0.25]" {
		t.Errorf("got %s", got)
	}
}
