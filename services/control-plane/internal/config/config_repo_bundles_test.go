package config

import (
	"strings"
	"testing"
)

// Materialization is opt-in AND all-or-nothing: half-configured GitHub
// credentials must refuse boot (G14 posture) rather than mint unscoped tokens.
func TestRepoBundlesConfigMatrix(t *testing.T) {
	baseEnv := map[string]string{
		"CISYNC_CTRL_ADMIN_TOKEN":       "admin-test-token",
		"CISYNC_CTRL_WEBHOOK_SECRET":    "whsec",
		"CISYNC_CTRL_PG_DSN":            "postgres://localhost/cisync_test",
		"CISYNC_CTRL_JOBLEASE_KEY_FILE": "",
	}

	clear := func(t *testing.T) {
		t.Helper()
		for _, k := range []string{
			"CISYNC_CTRL_REPO_BUNDLES_DIR", "CISYNC_CTRL_GITHUB_APP_ID",
			"CISYNC_CTRL_GITHUB_APP_KEY_FILE", "CISYNC_CTRL_GITHUB_INSTALLATION_ID",
			"CISYNC_CTRL_GITHUB_TOKEN",
		} {
			t.Setenv(k, "")
		}
	}
	fromBase := func(t *testing.T) *Config {
		t.Helper()
		for k, v := range baseEnv {
			t.Setenv(k, v)
		}
		return nil
	}
	expectErrContaining := func(t *testing.T, cfg *Config, err error, fragment string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("expected error containing %q, got err=%v", fragment, err)
		}
	}

	t.Run("disabled by default", func(t *testing.T) {
		clear(t)
		fromBase(t)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("base load: %v", err)
		}
		if cfg.RepoBundlesDir != "" || cfg.GitHubStaticToken != "" {
			t.Fatalf("materialization must stay disabled without envs: %+v", cfg.RepoBundlesDir)
		}
	})

	t.Run("static token enabled", func(t *testing.T) {
		clear(t)
		fromBase(t)
		t.Setenv("CISYNC_CTRL_REPO_BUNDLES_DIR", "/repos")
		t.Setenv("CISYNC_CTRL_GITHUB_TOKEN", "staged_token")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("static token config rejected: %v", err)
		}
		if cfg.GitHubStaticToken == "" || cfg.RepoBundlesDir != "/repos" {
			t.Fatalf("static config lost: %+v", cfg)
		}
	})

	t.Run("full app trio accepted", func(t *testing.T) {
		clear(t)
		fromBase(t)
		t.Setenv("CISYNC_CTRL_REPO_BUNDLES_DIR", "/repos")
		t.Setenv("CISYNC_CTRL_GITHUB_APP_ID", "998877")
		t.Setenv("CISYNC_CTRL_GITHUB_APP_KEY_FILE", "/keys/app.pem")
		t.Setenv("CISYNC_CTRL_GITHUB_INSTALLATION_ID", "4242")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("app trio config rejected: %v", err)
		}
		if cfg.GitHubAppID != 998877 || cfg.GitHubInstallationID != 4242 {
			t.Fatalf("trio not parsed: %+v", cfg)
		}
	})

	t.Run("half trio rejected at boot", func(t *testing.T) {
		clear(t)
		fromBase(t)
		t.Setenv("CISYNC_CTRL_REPO_BUNDLES_DIR", "/repos")
		t.Setenv("CISYNC_CTRL_GITHUB_APP_ID", "998877") // missing key file + installation id
		err := loadForTest()
		expectErrContaining(t, nil, err, "EITHER")
	})

	t.Run("token plus partial app rejected", func(t *testing.T) {
		clear(t)
		fromBase(t)
		t.Setenv("CISYNC_CTRL_REPO_BUNDLES_DIR", "/repos")
		t.Setenv("CISYNC_CTRL_GITHUB_TOKEN", "staged_token")
		t.Setenv("CISYNC_CTRL_GITHUB_INSTALLATION_ID", "4242")
		err := loadForTest()
		expectErrContaining(t, nil, err, "EITHER")
	})
}

func loadForTest() error {
	_, err := Load()
	return err
}
