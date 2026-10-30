// Command server runs the LCA dashboard backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"lca-backend/internal/api"
	"lca-backend/internal/asr"
	"lca-backend/internal/config"
	"lca-backend/internal/embed"
	"lca-backend/internal/events"
	"lca-backend/internal/kb"
	"lca-backend/internal/llm"
	"lca-backend/internal/session"
	"lca-backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, dbPing, err := openStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.SeedUsers(ctx, api.HashPassword); err != nil {
		return fmt.Errorf("seed users: %w", err)
	}

	var rdb *redis.Client
	if cfg.RedisURL == "" {
		slog.Warn("REDIS_URL not set; sessions and live events stay in memory")
	} else {
		redisOpts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("REDIS_URL: %w", err)
		}
		rdb = redis.NewClient(redisOpts)
		defer rdb.Close()
		if err := rdb.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("connect to redis: %w", err)
		}
	}

	var speechToText *asr.Client
	if cfg.DeepgramAPIKey == "" {
		slog.Warn("DEEPGRAM_API_KEY not set; Stream Audio uses the browser's speech recognition")
	} else {
		speechToText = asr.NewClient(cfg.DeepgramAPIKey, cfg.DeepgramModel)
		speechToText.Keyterms = asr.Keyterms(cfg.DeepgramKeyterms)
		slog.Info("live transcription via Deepgram", "model", speechToText.Model)
	}

	var knowledge *kb.Index
	if cfg.KBDir != "" {
		// With Postgres, passages and vectors live in pgvector; otherwise a cache file
		var vectors kb.VectorStore
		if pg, ok := db.(*store.Store); ok {
			pgLog, closeLog, err := fileLogger(cfg.PGVectorLog, "pgvector")
			if err != nil {
				return err
			}
			defer closeLog()
			if vs, err := kb.NewPGStore(ctx, pg.Pool(), pgLog); err != nil {
				slog.Warn("pgvector unavailable; knowledge base uses a cache file", "err", err)
			} else {
				vectors = vs
			}
		}
		knowledge = kb.New(cfg.KBDir, embed.NewClient(cfg.EmbedURL, cfg.EmbedModel), vectors)
		// Embedding can take a while on first start; don't hold up the server
		go func() {
			if err := knowledge.Build(ctx); err != nil {
				slog.Warn("knowledge base not ready; it will retry on the first question", "err", err)
			}
		}()
	}

	// Generation step of RAG: a local chat model writes answers from passages
	var writer api.Writer
	if knowledge != nil && cfg.LLMModel != "off" {
		writer = llm.NewClient(cfg.EmbedURL, cfg.LLMModel)
		slog.Info("knowledge base answers written by", "model", cfg.LLMModel)
	}

	hub := events.NewHub(rdb)
	go hub.Run(ctx)

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: api.New(api.Options{
			Store:         db,
			Sessions:      session.NewStore(rdb),
			Hub:           hub,
			ASR:           speechToText,
			Phone:         cfg.Phone,
			KB:            knowledge,
			Writer:        writer,
			WriterModel:   cfg.LLMModel,
			IngestToken:   cfg.IngestToken,
			CookieSecure:  cfg.CookieSecure,
			StaticDir:     cfg.StaticDir,
			Recordings:    cfg.RecordingsDir,
			CategoryAlert: cfg.CategoryAlert,
			Recategorize:  true,
			Health: func(ctx context.Context) error {
				if rdb == nil {
					return dbPing(ctx)
				}
				return errors.Join(dbPing(ctx), rdb.Ping(ctx).Err())
			},
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// fileLogger logs to the console and also appends to path, tagged with
// component; path "off" logs to the console only.
func fileLogger(path, component string) (*slog.Logger, func(), error) {
	if path == "off" {
		return slog.Default().With("component", component), func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("%s log: %w", component, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("%s log: %w", component, err)
	}
	slog.Info("logging "+component+" to", "file", path)
	log := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, f), nil)).With("component", component)
	return log, func() { f.Close() }, nil
}

type seededStore interface {
	api.Store
	SeedUsers(ctx context.Context, hash func(string) (string, error)) error
	Close()
}

// openStore connects to Postgres, or falls back to memory when databaseURL is
// empty. The returned ping reports database health.
func openStore(ctx context.Context, databaseURL string) (seededStore, func(context.Context) error, error) {
	if databaseURL == "" {
		slog.Warn("DATABASE_URL not set; users and calls stay in memory and reset on restart")
		return store.NewMemory(), func(context.Context) error { return nil }, nil
	}
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := db.Migrate(ctx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	return db, db.Ping, nil
}
