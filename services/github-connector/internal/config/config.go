// Package config parses all SAURON_CONN_* environment variables. It is the
// only place in this service that reads the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config carries every tunable for the github-connector service.
type Config struct {
	Addr          string
	PGDSN         string
	WebhookSecret string
	DetailsURL    string

	// GitHub App credentials. When ALL are absent the connector runs in
	// dry-run mode (checks are logged, never published). When only some are
	// present Load fails: half-configured credentials must not silently
	// degrade into dry-run.
	GitHubAppID             string
	GitHubAppPrivateKeyFile string
	GitHubInstallationID    int64

	DryRun bool
}

// Load builds a Config from SAURON_CONN_* variables.
func Load() (*Config, error) {
	cfg := &Config{
		Addr:                    envOr("SAURON_CONN_ADDR", ":8083"),
		PGDSN:                   os.Getenv("SAURON_CONN_PG_DSN"),
		WebhookSecret:           os.Getenv("SAURON_CONN_WEBHOOK_SECRET"),
		DetailsURL:              envOr("SAURON_CONN_DETAILS_URL", "http://localhost:3000"),
		GitHubAppID:             os.Getenv("SAURON_CONN_GITHUB_APP_ID"),
		GitHubAppPrivateKeyFile: os.Getenv("SAURON_CONN_GITHUB_APP_PRIVATE_KEY_FILE"),
	}
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("config: SAURON_CONN_WEBHOOK_SECRET is required")
	}
	rawInstall := os.Getenv("SAURON_CONN_GITHUB_INSTALLATION_ID")
	if rawInstall != "" {
		id, err := strconv.ParseInt(rawInstall, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("config: invalid SAURON_CONN_GITHUB_INSTALLATION_ID %q", rawInstall)
		}
		cfg.GitHubInstallationID = id
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
		return nil, fmt.Errorf("config: SAURON_CONN_GITHUB_APP_ID, SAURON_CONN_GITHUB_APP_PRIVATE_KEY_FILE and SAURON_CONN_GITHUB_INSTALLATION_ID must be configured together")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
