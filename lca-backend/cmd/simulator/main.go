// Command simulator plays demo calls into a running backend so the dashboard
// shows live calls. It reads INGEST_TOKEN and PORT from the same .env as the server.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"lca-backend/internal/config"
	"lca-backend/internal/simulator"
)

func main() {
	if err := run(); err != nil {
		slog.Error("simulator stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	url := flag.String("url", "http://localhost:"+cfg.Port, "backend base URL")
	agents := flag.String("agents", "BobS,BabuS", "comma-separated agent IDs to route calls to")
	concurrent := flag.Int("concurrent", 2, "calls in progress at once")
	total := flag.Int("calls", 0, "calls to play before exiting; 0 runs until Ctrl+C")
	speed := flag.Float64("speed", 1, "playback speed; 2 plays calls twice as fast")
	gap := flag.Duration("gap", 5*time.Second, "pause between calls on each line")
	flag.Parse()

	agentList := strings.Split(*agents, ",")
	if *concurrent < 1 || *agents == "" {
		return errors.New("need at least one agent and -concurrent >= 1")
	}
	scripts, err := simulator.Scripts()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	player := &simulator.Player{
		Client: &simulator.Client{BaseURL: *url, Token: cfg.IngestToken, HTTP: &http.Client{Timeout: 10 * time.Second}},
		Speed:  *speed,
	}

	// Each worker is one "phone line" playing calls back to back
	var started int
	var mu sync.Mutex
	next := func() bool {
		mu.Lock()
		defer mu.Unlock()
		if *total > 0 && started >= *total {
			return false
		}
		started++
		return true
	}

	slog.Info("simulating calls", "url", *url, "agents", agentList, "concurrent", *concurrent, "speed", *speed)
	var wg sync.WaitGroup
	for line := range *concurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil && next() {
				s := scripts[rand.IntN(len(scripts))]
				agent := strings.TrimSpace(agentList[rand.IntN(len(agentList))])
				id, err := player.Play(ctx, agent, s)
				if err != nil {
					slog.Error("call failed", "line", line, "script", s.Name, "call", id, "err", err)
				} else {
					slog.Info("call finished", "line", line, "script", s.Name, "agent", agent, "call", id)
				}
				select {
				case <-ctx.Done():
				case <-time.After(*gap):
				}
			}
		}()
	}
	wg.Wait()
	return nil
}
