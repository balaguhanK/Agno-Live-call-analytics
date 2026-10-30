// Package config loads server settings from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/joho/godotenv"

	"lca-backend/internal/phone"
)

type Config struct {
	Port         string
	DatabaseURL  string // optional; empty keeps users and calls in memory
	RedisURL     string // optional; empty keeps sessions and events in memory
	IngestToken  string // bearer token for FreeSWITCH / ASR workers pushing call data
	CookieSecure bool   // set true when served over HTTPS
	StaticDir    string // optional built dashboard to serve, e.g. ../lca-dashboard/dist
	// Optional; without a key the dashboard falls back to browser speech recognition
	DeepgramAPIKey string
	DeepgramModel  string
	// Extra words for the speech model to favour, comma separated (on top of the built-in list)
	DeepgramKeyterms string
	// Optional browser phone through FreeSWITCH; disabled unless all are set
	Phone phone.Config
	// Agent Assist knowledge base: a folder of .md/.txt documents, embedded
	// with a local Ollama model. Empty KBDir disables it.
	KBDir      string
	EmbedURL   string
	EmbedModel string
	// Chat model that writes knowledge-base answers; "off" quotes passages instead
	LLMModel string
	// File that pgvector operations are also logged to; "off" logs to the console only
	PGVectorLog string
	// Folder where call recordings uploaded by the browser phone are kept
	RecordingsDir string
	// Call categories whose names match this are alerts, shown in red and
	// counted in the call's alerts (LCA's Category Alert Regular Expression)
	CategoryAlert *regexp.Regexp
}

// DefaultCategoryAlert makes a manager escalation the only alert, as in LCA's sample
const DefaultCategoryAlert = "ManagerEscalation"

// Load reads an env file (LCA_ENV_FILE, else ../lca-dashboard/.env, else .env)
// without overriding variables already set in the process environment.
func Load() (Config, error) {
	envFile := os.Getenv("LCA_ENV_FILE")
	if envFile == "" {
		for _, candidate := range []string{"../lca-dashboard/.env", ".env"} {
			if _, err := os.Stat(candidate); err == nil {
				envFile = candidate
				break
			}
		}
	}
	if envFile != "" {
		if err := godotenv.Load(envFile); err != nil {
			return Config{}, fmt.Errorf("load %s: %w", envFile, err)
		}
	}

	cfg := Config{
		Port:        getenv("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		IngestToken: os.Getenv("INGEST_TOKEN"),
		StaticDir:   os.Getenv("STATIC_DIR"),

		DeepgramAPIKey:   os.Getenv("DEEPGRAM_API_KEY"),
		DeepgramModel:    os.Getenv("DEEPGRAM_MODEL"),
		DeepgramKeyterms: os.Getenv("DEEPGRAM_KEYTERMS"),

		KBDir:      getenv("KB_DIR", "knowledge"),
		EmbedURL:   os.Getenv("EMBED_URL"),
		EmbedModel: os.Getenv("EMBED_MODEL"),
		LLMModel:   getenv("LLM_MODEL", "gemma2:2b"),

		PGVectorLog: getenv("PGVECTOR_LOG", "logs/pgvector.log"),

		RecordingsDir: getenv("RECORDINGS_DIR", "recordings"),

		Phone: phone.Config{
			WSURL:       os.Getenv("FS_WS_URL"),
			Domain:      os.Getenv("FS_SIP_DOMAIN"),
			QueueNumber: getenv("FS_QUEUE_NUMBER", "5000"),
			Secret:      os.Getenv("FS_SIP_SECRET"),
		},
	}
	cfg.CookieSecure, _ = strconv.ParseBool(os.Getenv("COOKIE_SECURE"))

	var missing []error
	if len(cfg.IngestToken) < 16 {
		missing = append(missing, errors.New("INGEST_TOKEN must be at least 16 characters"))
	}
	alert, err := regexp.Compile(getenv("CATEGORY_ALERT_REGEX", DefaultCategoryAlert))
	if err != nil {
		missing = append(missing, fmt.Errorf("CATEGORY_ALERT_REGEX: %w", err))
	}
	cfg.CategoryAlert = alert
	return cfg, errors.Join(missing...)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
