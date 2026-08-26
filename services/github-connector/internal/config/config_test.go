package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func setEnvs(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

var envKeys = []string{
	"CISYNC_CONN_WEBHOOK_SECRET", "CISYNC_CONN_GITHUB_APP_ID",
	"CISYNC_CONN_GITHUB_PRIVATE_KEY_FILE", "CISYNC_CONN_GITHUB_INSTALLATION_ID",
	"CISYNC_CONN_RERUN_POLICY", "CISYNC_CONN_RERUN_MAX_PER_CANDIDATE",
	"CISYNC_CONN_RERUN_RATE_PER_HOUR", "CISYNC_CONN_WRITE_BUDGET_PER_HOUR",
	"CISYNC_CONN_STALLED_CHECK_AGE", "CISYNC_CONN_SWEEP_INTERVAL",
	"CISYNC_CONN_PENDING_DRAIN_INTERVAL", "CISYNC_CONN_CTRL_URL", "CISYNC_CONN_CTRL_TOKEN",
	"CISYNC_CONN_GITHUB_APP_PRIVATE_KEY_FILE",
}

func TestDefaultsAreDryRunWithFrozenKnobs(t *testing.T) {
	setEnvs(t, map[string]string{"CISYNC_CONN_WEBHOOK_SECRET": "s"})
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.DryRun)
	require.Equal(t, 300, cfg.WriteBudgetPerHour)
	require.Equal(t, 2, cfg.RerunMaxPerCandidate)
	require.Equal(t, 20, cfg.RerunRatePerHour)
	require.Equal(t, 45*time.Minute, cfg.StalledCheckAge)
	require.False(t, cfg.CtrlBaseURL != "", "ctrl URL unset by default")
}

func TestCredTrioAllOrNothing(t *testing.T) {
	setEnvs(t, map[string]string{
		"CISYNC_CONN_WEBHOOK_SECRET":         "s",
		"CISYNC_CONN_GITHUB_APP_ID":          "app_1",
		"CISYNC_CONN_GITHUB_INSTALLATION_ID": "42",
	})
	_, err := Load()
	require.Error(t, err, "half-configured credentials must fail loudly")

	setEnvs(t, map[string]string{
		"CISYNC_CONN_WEBHOOK_SECRET":          "s",
		"CISYNC_CONN_GITHUB_APP_ID":           "app_1",
		"CISYNC_CONN_GITHUB_PRIVATE_KEY_FILE": "/keys/pem",
		"CISYNC_CONN_GITHUB_INSTALLATION_ID":  "42",
	})
	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.DryRun)
	require.Equal(t, int64(42), cfg.GitHubInstallationID)
}

func TestRerunPolicyAndBudgetOverrides(t *testing.T) {
	setEnvs(t, map[string]string{
		"CISYNC_CONN_WEBHOOK_SECRET":        "s",
		"CISYNC_CONN_RERUN_POLICY":          "replay_cached",
		"CISYNC_CONN_WRITE_BUDGET_PER_HOUR": "50",
		"CISYNC_CONN_STALLED_CHECK_AGE":     "10m",
		"CISYNC_CONN_CTRL_URL":              "http://ctrl:8081",
		"CISYNC_CONN_CTRL_TOKEN":            "tok",
	})
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "replay_cached", string(cfg.RerunPolicy))
	require.Equal(t, 50, cfg.WriteBudgetPerHour)
	require.Equal(t, 10*time.Minute, cfg.StalledCheckAge)
	require.Equal(t, "http://ctrl:8081", cfg.CtrlBaseURL)
	require.Equal(t, "tok", cfg.CtrlToken)

	setEnvs(t, map[string]string{
		"CISYNC_CONN_WEBHOOK_SECRET": "s",
		"CISYNC_CONN_RERUN_POLICY":   "yolo",
	})
	_, err = Load()
	require.Error(t, err)

	setEnvs(t, map[string]string{
		"CISYNC_CONN_WEBHOOK_SECRET":    "s",
		"CISYNC_CONN_STALLED_CHECK_AGE": "not-a-duration",
	})
	_, err = Load()
	require.Error(t, err)
}
