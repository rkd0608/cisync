// Package config parses all SAURON_FLEET_* environment variables. It is the
// only place in this service that reads the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config carries every tunable for the runner-fleet service.
type Config struct {
	Addr              string
	PGDSN             string
	Provider          string
	Pool              string
	SimWorkers        int
	HeartbeatInterval time.Duration
	WorkerStaleAfter  time.Duration
	PollInterval      time.Duration
	ClaimLimit        int
	GaugeInterval     time.Duration
	DockerBin         string
	DockerImage       string
}

// FromEnv builds a Config from SAURON_FLEET_* variables with documented
// defaults; SAURON_FLEET_PROVIDER selects sim (default) or docker (opt-in).
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:              envOr("SAURON_FLEET_ADDR", ":8082"),
		PGDSN:             os.Getenv("SAURON_FLEET_PG_DSN"),
		Provider:          envOr("SAURON_FLEET_PROVIDER", "sim"),
		Pool:              envOr("SAURON_FLEET_POOL", "sim"),
		SimWorkers:        8,
		HeartbeatInterval: 5 * time.Second,
		WorkerStaleAfter:  15 * time.Second,
		PollInterval:      100 * time.Millisecond,
		ClaimLimit:        4,
		GaugeInterval:     5 * time.Second,
		DockerBin:         "docker",
		DockerImage:       "docker.io/library/busybox:1.36",
	}
	switch cfg.Provider {
	case "sim", "docker":
	default:
		return cfg, fmt.Errorf("config: invalid SAURON_FLEET_PROVIDER %q (sim|docker)", cfg.Provider)
	}
	var err error
	if cfg.SimWorkers, err = intEnv("SAURON_FLEET_SIM_WORKERS", cfg.SimWorkers); err != nil {
		return cfg, err
	}
	if cfg.ClaimLimit, err = intEnv("SAURON_FLEET_CLAIM_LIMIT", cfg.ClaimLimit); err != nil {
		return cfg, err
	}
	if cfg.HeartbeatInterval, err = durEnv("SAURON_FLEET_HEARTBEAT_INTERVAL", cfg.HeartbeatInterval); err != nil {
		return cfg, err
	}
	if cfg.WorkerStaleAfter, err = durEnv("SAURON_FLEET_WORKER_STALE_AFTER", cfg.WorkerStaleAfter); err != nil {
		return cfg, err
	}
	if cfg.PollInterval, err = durEnv("SAURON_FLEET_POLL_INTERVAL", cfg.PollInterval); err != nil {
		return cfg, err
	}
	if v := os.Getenv("SAURON_FLEET_DOCKER_BIN"); v != "" {
		cfg.DockerBin = v
	}
	if v := os.Getenv("SAURON_FLEET_DOCKER_IMAGE"); v != "" {
		cfg.DockerImage = v
	}
	return cfg, nil
}

func intEnv(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback, fmt.Errorf("config: invalid %s %q", key, v)
	}
	return n, nil
}

func durEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback, fmt.Errorf("config: invalid %s %q", key, v)
	}
	return d, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
