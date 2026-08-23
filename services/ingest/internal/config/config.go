// Package config parses all SAURON_INGEST_* environment variables. It is the
// only place in this service that reads the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config carries every tunable for the ingest service.
type Config struct {
	Addr          string
	PGDSN         string
	WebhookSecret string
	ControlURL    string
	MaxBodyBytes  int64
	TimestampSkew time.Duration
	RetryInterval time.Duration
	RetryBase     time.Duration
	RetryMaxDelay time.Duration
	MaxAttempts   int
}

// FromEnv builds a Config from SAURON_INGEST_* variables, applying documented
// defaults for anything unset or malformed.
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:          envOr("SAURON_INGEST_ADDR", ":8080"),
		PGDSN:         os.Getenv("SAURON_INGEST_PG_DSN"),
		WebhookSecret: os.Getenv("SAURON_INGEST_WEBHOOK_SECRET"),
		ControlURL:    envOr("SAURON_INGEST_CTRL_URL", "http://localhost:8081"),
		MaxBodyBytes:  int64(25 << 20),
		TimestampSkew: 5 * time.Minute,
		RetryInterval: 5 * time.Second,
		RetryBase:     10 * time.Second,
		RetryMaxDelay: 5 * time.Minute,
		MaxAttempts:   12,
	}
	if cfg.WebhookSecret == "" {
		return cfg, fmt.Errorf("config: SAURON_INGEST_WEBHOOK_SECRET is required")
	}
	if v := os.Getenv("SAURON_INGEST_MAX_BODY_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("config: invalid SAURON_INGEST_MAX_BODY_BYTES %q", v)
		}
		cfg.MaxBodyBytes = n
	}
	if v := os.Getenv("SAURON_INGEST_RETRY_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("config: invalid SAURON_INGEST_RETRY_INTERVAL %q", v)
		}
		cfg.RetryInterval = d
	}
	if v := os.Getenv("SAURON_INGEST_MAX_ATTEMPTS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("config: invalid SAURON_INGEST_MAX_ATTEMPTS %q", v)
		}
		cfg.MaxAttempts = n
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
