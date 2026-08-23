package config

import (
	"testing"
	"time"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("SAURON_FLEET_PROVIDER", "")
	t.Setenv("SAURON_FLEET_ADDR", "")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("defaults must parse: %v", err)
	}
	if cfg.Provider != "sim" || cfg.Addr != ":8082" || cfg.Pool != "sim" {
		t.Fatalf("wrong defaults: %+v", cfg)
	}
	if cfg.SimWorkers != 8 || cfg.ClaimLimit != 4 {
		t.Fatalf("worker defaults wrong: %+v", cfg)
	}
	if cfg.HeartbeatInterval != 5*time.Second {
		t.Fatalf("heartbeat default must be 5s, got %s", cfg.HeartbeatInterval)
	}
}

func TestFromEnvDockerOptIn(t *testing.T) {
	t.Setenv("SAURON_FLEET_PROVIDER", "docker")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("docker must be accepted: %v", err)
	}
	if cfg.DockerImage == "" || cfg.DockerBin == "" {
		t.Fatalf("docker defaults missing: %+v", cfg)
	}
}

func TestFromEnvRejectsUnknownProvider(t *testing.T) {
	t.Setenv("SAURON_FLEET_PROVIDER", "firecracker")
	if _, err := FromEnv(); err == nil {
		t.Fatalf("unknown provider must fail closed")
	}
}

func TestFromEnvOverrides(t *testing.T) {
	t.Setenv("SAURON_FLEET_SIM_WORKERS", "3")
	t.Setenv("SAURON_FLEET_CLAIM_LIMIT", "7")
	t.Setenv("SAURON_FLEET_HEARTBEAT_INTERVAL", "2s")
	t.Setenv("SAURON_FLEET_WORKER_STALE_AFTER", "9s")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("overrides must parse: %v", err)
	}
	if cfg.SimWorkers != 3 || cfg.ClaimLimit != 7 ||
		cfg.HeartbeatInterval != 2*time.Second || cfg.WorkerStaleAfter != 9*time.Second {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

func TestFromEnvRejectsGarbageNumbers(t *testing.T) {
	t.Setenv("SAURON_FLEET_SIM_WORKERS", "zero")
	if _, err := FromEnv(); err == nil {
		t.Fatalf("garbage workers must error")
	}
}
