// Command srs runs the SIPREC Session Recording Server: it receives call
// audio from an SRC (SBC/PBX), transcribes each participant with Deepgram and
// posts the lines to the dashboard backend. Settings come from the same .env
// as the backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"lca-backend/internal/asr"
	"lca-backend/internal/config"
	"lca-backend/internal/srs"
)

func main() {
	if err := run(); err != nil {
		slog.Error("recorder stopped", "err", err)
		os.Exit(1)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getint(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	return n, nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DeepgramAPIKey == "" {
		return errors.New("DEEPGRAM_API_KEY is required for the recorder")
	}
	sipPort, err := getint("SRS_SIP_PORT", 5090)
	if err != nil {
		return err
	}
	rtpMin, err := getint("SRS_RTP_PORT_MIN", 30000)
	if err != nil {
		return err
	}
	rtpMax, err := getint("SRS_RTP_PORT_MAX", 30200)
	if err != nil {
		return err
	}
	rtpTimeout, err := time.ParseDuration(getenv("SRS_RTP_TIMEOUT", "30s"))
	if err != nil {
		return fmt.Errorf("SRS_RTP_TIMEOUT: %w", err)
	}
	bindIP := getenv("SRS_BIND_IP", "0.0.0.0")
	mediaIP := getenv("SRS_MEDIA_IP", "127.0.0.1")

	server, err := srs.New(srs.Config{
		MediaIP: mediaIP, RTPPortMin: rtpMin, RTPPortMax: rtpMax,
		Language: getenv("SRS_LANGUAGE", "en-IN"), RTPTimeout: rtpTimeout,
	},
		asr.NewClient(cfg.DeepgramAPIKey, cfg.DeepgramModel),
		&srs.Backend{
			BaseURL: getenv("BACKEND_URL", "http://localhost:"+cfg.Port),
			Token:   cfg.IngestToken,
			HTTP:    &http.Client{Timeout: 10 * time.Second},
		})
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(bindIP, strconv.Itoa(sipPort))
	udp, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("SIP UDP %s: %w", addr, err)
	}
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		udp.Close()
		return fmt.Errorf("SIP TCP %s: %w", addr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Serve(ctx, udp, tcp)
}
