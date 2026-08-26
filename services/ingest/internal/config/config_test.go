package config

import (
	"testing"
	"time"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("CISYNC_INGEST_WEBHOOK_SECRET", "s")
	t.Setenv("CISYNC_INGEST_ADDR", "")
	t.Setenv("CISYNC_INGEST_CTRL_URL", "")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("defaults must parse: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("default addr wrong: %s", cfg.Addr)
	}
	if cfg.ControlURL != "http://localhost:8081" {
		t.Fatalf("default ctrl url wrong: %s", cfg.ControlURL)
	}
	if cfg.MaxBodyBytes != 25<<20 {
		t.Fatalf("default body cap wrong: %d", cfg.MaxBodyBytes)
	}
	if cfg.TimestampSkew != 5*time.Minute {
		t.Fatalf("default skew wrong: %s", cfg.TimestampSkew)
	}
}

func TestFromEnvRequiresSecret(t *testing.T) {
	if _, err := FromEnv(); err == nil {
		t.Fatalf("missing webhook secret must fail closed")
	}
}

func TestFromEnvOverrides(t *testing.T) {
	t.Setenv("CISYNC_INGEST_WEBHOOK_SECRET", "s")
	t.Setenv("CISYNC_INGEST_ADDR", ":9090")
	t.Setenv("CISYNC_INGEST_MAX_BODY_BYTES", "1024")
	t.Setenv("CISYNC_INGEST_MAX_ATTEMPTS", "5")
	t.Setenv("CISYNC_INGEST_RETRY_INTERVAL", "2s")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("overrides must parse: %v", err)
	}
	if cfg.Addr != ":9090" || cfg.MaxBodyBytes != 1024 || cfg.MaxAttempts != 5 || cfg.RetryInterval != 2*time.Second {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

func TestFromEnvRejectsGarbage(t *testing.T) {
	t.Setenv("CISYNC_INGEST_WEBHOOK_SECRET", "s")
	t.Setenv("CISYNC_INGEST_MAX_BODY_BYTES", "notanumber")
	if _, err := FromEnv(); err == nil {
		t.Fatalf("garbage max body bytes must error")
	}
}
