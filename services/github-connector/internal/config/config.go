// Package config parses all CISYNC_CONN_* environment variables. It is the
// only place in this service that reads the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"cisync.dev/cisync/github-connector/internal/rerun"
)

// Config carries every tunable for the github-connector service.
type Config struct {
	Addr          string
	PGDSN         string
	WebhookSecret string
	DetailsURL    string
	// AdminToken guards GET /v1/installations/status with the same bearer
	// pattern as control-plane. Empty ⇒ that endpoint fails closed (401 for
	// every caller) rather than exposing installation data unauthenticated.
	AdminToken string

	// GitHub App credentials. When ALL are absent the connector runs in
	// dry-run mode (checks are logged, never published). When only some are
	// present Load fails: half-configured credentials must not silently
	// degrade into dry-run.
	GitHubAppID             string
	GitHubAppPrivateKeyFile string
	GitHubInstallationID    int64

	// Re-run policy knobs (plan §4.5 / frozen ruling §10.2).
	RerunPolicy          rerun.Policy
	RerunMaxPerCandidate int
	RerunRatePerHour     int

	// Write budget + stalled safety net (plan §4.6/§4.2).
	WriteBudgetPerHour   int
	StalledCheckAge      time.Duration
	SweepInterval        time.Duration
	PendingDrainInterval time.Duration

	// Control-plane base URL + bearer token for POST
	// /v1/candidates/{id}/revalidate; empty URL disables replan (feature
	// flag off) and re-runs surface as neutral "unavailable".
	CtrlBaseURL string
	CtrlToken   string

	// ReportComments opts into the sticky PR verification comment (W6).
	// Requires the GitHub App to grant Issues: Read & write BEFORE flipping;
	// see internal-protocols §4.1 + RUNBOOK "Sticky PR comments".
	ReportComments bool

	DryRun bool
}

// Load builds a Config from CISYNC_CONN_* variables.
func Load() (*Config, error) {
	cfg := &Config{
		Addr:                    envOr("CISYNC_CONN_ADDR", ":8083"),
		PGDSN:                   os.Getenv("CISYNC_CONN_PG_DSN"),
		WebhookSecret:           os.Getenv("CISYNC_CONN_WEBHOOK_SECRET"),
		DetailsURL:              envOr("CISYNC_CONN_DETAILS_URL", "http://localhost:3000"),
		AdminToken:              os.Getenv("CISYNC_CONN_ADMIN_TOKEN"),
		GitHubAppID:             os.Getenv("CISYNC_CONN_GITHUB_APP_ID"),
		GitHubAppPrivateKeyFile: os.Getenv("CISYNC_CONN_GITHUB_PRIVATE_KEY_FILE"),
	}
	if cfg.GitHubAppPrivateKeyFile == "" {
		cfg.GitHubAppPrivateKeyFile = os.Getenv("CISYNC_CONN_GITHUB_APP_PRIVATE_KEY_FILE")
	}
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("config: CISYNC_CONN_WEBHOOK_SECRET is required")
	}
	if err := cfg.loadInstallation(); err != nil {
		return nil, err
	}

	present := 0
	for _, v := range []string{cfg.GitHubAppID, cfg.GitHubAppPrivateKeyFile} {
		if v != "" {
			present++
		}
	}
	if cfg.GitHubInstallationID != 0 {
		present++
	}
	switch present {
	case 0:
		cfg.DryRun = true
	case 3:
		cfg.DryRun = false
	default:
		return nil, fmt.Errorf("config: CISYNC_CONN_GITHUB_APP_ID, CISYNC_CONN_GITHUB_PRIVATE_KEY_FILE and CISYNC_CONN_GITHUB_INSTALLATION_ID must be configured together")
	}

	policy, err := rerun.ParsePolicy(os.Getenv("CISYNC_CONN_RERUN_POLICY"))
	if err != nil {
		return nil, err
	}
	cfg.RerunPolicy = policy
	if cfg.RerunMaxPerCandidate, err = envInt("CISYNC_CONN_RERUN_MAX_PER_CANDIDATE", 2); err != nil {
		return nil, err
	}
	if cfg.RerunRatePerHour, err = envInt("CISYNC_CONN_RERUN_RATE_PER_HOUR", 20); err != nil {
		return nil, err
	}
	if cfg.WriteBudgetPerHour, err = envInt("CISYNC_CONN_WRITE_BUDGET_PER_HOUR", 300); err != nil {
		return nil, err
	}
	if cfg.StalledCheckAge, err = envDuration("CISYNC_CONN_STALLED_CHECK_AGE", 45*time.Minute); err != nil {
		return nil, err
	}
	if cfg.SweepInterval, err = envDuration("CISYNC_CONN_SWEEP_INTERVAL", time.Minute); err != nil {
		return nil, err
	}
	if cfg.PendingDrainInterval, err = envDuration("CISYNC_CONN_PENDING_DRAIN_INTERVAL", 30*time.Second); err != nil {
		return nil, err
	}
	if cfg.RerunMaxPerCandidate <= 0 || cfg.RerunRatePerHour <= 0 || cfg.WriteBudgetPerHour <= 0 {
		return nil, fmt.Errorf("config: rerun/write-budget knobs must be positive")
	}
	if cfg.StalledCheckAge <= 0 || cfg.SweepInterval <= 0 || cfg.PendingDrainInterval <= 0 {
		return nil, fmt.Errorf("config: interval knobs must be positive")
	}
	cfg.CtrlBaseURL = os.Getenv("CISYNC_CONN_CTRL_URL")
	cfg.CtrlToken = os.Getenv("CISYNC_CONN_CTRL_TOKEN")
	if cfg.ReportComments, err = envBool("CISYNC_CONN_REPORT_COMMENTS", false); err != nil {
		return nil, err
	}
	return cfg, nil
}

// envBool parses strict booleans: only 1/true/yes/on (case-insensitive) and
// their negations are valid; a typo'd value fails instead of defaulting.
func envBool(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("config: invalid %s %q (want true/false)", key, raw)
	}
}

func (c *Config) loadInstallation() error {
	rawInstall := os.Getenv("CISYNC_CONN_GITHUB_INSTALLATION_ID")
	if rawInstall == "" {
		return nil
	}
	id, err := strconv.ParseInt(rawInstall, 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("config: invalid CISYNC_CONN_GITHUB_INSTALLATION_ID %q", rawInstall)
	}
	c.GitHubInstallationID = id
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: invalid %s %q", key, raw)
	}
	return v, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("config: invalid %s %q", key, raw)
	}
	return v, nil
}
