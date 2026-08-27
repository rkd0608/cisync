package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DevTenant is the tenant claimed by the admin token in dev posture. It MUST
// satisfy the frozen prefixedUlid pattern (events.schema.json): "org_" plus a
// 26-char Crockford base32 ULID. "org_default" violated the envelope schema
// and poisoned every served event.
const DevTenant = "org_01ARZ3NDEKTSV4RRFFQ69G5FAV"

// Config is the control-plane's full environment-derived configuration.
type Config struct {
	PGDSN         string
	Addr          string
	FleetURL      string
	AdminToken    string
	WebhookSecret string
	LedgerKeyFile string
	// JobLeaseKeyFile is the DEDICATED Ed25519 private key for job-lease
	// tokens (THREAT_MODEL B2/I-04) — never the ledger key: compromise of
	// either must not cascade into the other trust domain.
	JobLeaseKeyFile string
	// SessionKeyFile is the DEDICATED Ed25519 private key for stateless web
	// session JWTs (email+password sign-in, SPEC §3 2026-08-26). A THIRD key:
	// ledger != joblease != session so no single-key compromise cascades.
	// Optional at boot; when empty /v1/auth/* fails closed (503 mint-time).
	SessionKeyFile string
	TenantID       string

	RelayBatchSize    int
	RelayPollInterval time.Duration
	TickInterval      time.Duration
	ReconcileInterval time.Duration
	DefaultLeaseTTL   time.Duration
	RateLimitPerMin   int
	// StaleRunMaxAge bounds how long a dispatched run may sit without a
	// completion before the reconciler fences it (frees WIP slots). The
	// compiled default keeps the documented 2×15-min prod posture; dev
	// compose lowers it because sim jobs finish in seconds and an orphaned
	// dispatch otherwise wedges admission until restart+30min.
	StaleRunMaxAge time.Duration
	// SchedBatch bounds per-tick admission/dispatch work; dev compose raises
	// it so suite fan-out drains inside harness decision windows.
	SchedBatch int
	// TrackedBaseBranches names the push refs that count as base advances
	// (plan §5.1, ruling #4): default "main,master".
	TrackedBaseBranches []string
	// RerunMaxPerCandidate caps POST /candidates/{id}/revalidate per
	// candidate (ruling #2: default 2).
	RerunMaxPerCandidate int
	// VerifyInterval drives the in-process nightly chain verifier (H3):
	// 0 disables the loop (the `verify` subcommand stays available); prod
	// sets 24h per the I-07 nightly posture.
	VerifyInterval time.Duration
	// AuditRetentionDays bounds ctrl.security_audit row age (B7 >=90d);
	// pruned by the reconciler. Default 90.
	AuditRetentionDays int

	// Repo bundle materialization (realexec evidence sourcing; opt-in).
	// RepoBundlesDir non-empty enables the dispatch-path materializer that
	// stages head-state archives for the shared cisync-repos volume (runners
	// never hold GitHub tokens — THREAT_MODEL B5).
	RepoBundlesDir       string
	GitHubAppID          int64
	GitHubAppKeyFile     string
	GitHubInstallationID int64
	// GitHubStaticToken is the documented fallback credential source; the
	// GitHub App trio is preferred because installation tokens can be scoped
	// per-repo with contents:read only.
	GitHubStaticToken string
}

// parseList splits a comma-separated env list, trimming whitespace and
// dropping empties.
func parseList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v := env(key, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config %s: %w", key, err)
	}
	return n, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := env(key, "")
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config %s: %w", key, err)
	}
	return d, nil
}

// Load parses CISYNC_CTRL_* environment variables into Config; required vars
// error out when missing.
func Load() (*Config, error) {
	cfg := &Config{
		PGDSN:           env("CISYNC_CTRL_PG_DSN", "postgres://cisync:cisync_dev_only@localhost:5432/cisync"),
		Addr:            env("CISYNC_CTRL_ADDR", ":8081"),
		FleetURL:        env("CISYNC_CTRL_FLEET_URL", "http://localhost:8082"),
		AdminToken:      env("CISYNC_CTRL_ADMIN_TOKEN", ""),
		WebhookSecret:   env("CISYNC_CTRL_WEBHOOK_SECRET", env("CISYNC_INGEST_WEBHOOK_SECRET", "")),
		LedgerKeyFile:   env("CISYNC_CTRL_LEDGER_KEY_FILE", ""),
		JobLeaseKeyFile: env("CISYNC_CTRL_JOBLEASE_KEY_FILE", ""),
		SessionKeyFile:  env("CISYNC_SESSION_KEY_FILE", ""),
		TenantID:        env("CISYNC_CTRL_TENANT_ID", DevTenant),
	}
	var err error
	if cfg.RelayBatchSize, err = envInt("CISYNC_CTRL_RELAY_BATCH", 100); err != nil {
		return nil, err
	}
	if cfg.RelayPollInterval, err = envDuration("CISYNC_CTRL_RELAY_POLL", 500*time.Millisecond); err != nil {
		return nil, err
	}
	if cfg.TickInterval, err = envDuration("CISYNC_CTRL_TICK_INTERVAL", time.Second); err != nil {
		return nil, err
	}
	if cfg.ReconcileInterval, err = envDuration("CISYNC_CTRL_RECONCILE_INTERVAL", 30*time.Second); err != nil {
		return nil, err
	}
	if cfg.StaleRunMaxAge, err = envDuration("CISYNC_CTRL_STALE_RUN_MAX_AGE", 30*time.Minute); err != nil {
		return nil, err
	}
	if cfg.DefaultLeaseTTL, err = envDuration("CISYNC_CTRL_LEASE_TTL", 1500*time.Second); err != nil {
		return nil, err
	}
	if cfg.RateLimitPerMin, err = envInt("CISYNC_CTRL_RATE_LIMIT_PER_MIN", 120); err != nil {
		return nil, err
	}
	if cfg.SchedBatch, err = envInt("CISYNC_CTRL_SCHED_BATCH", 8); err != nil {
		return nil, err
	}
	if cfg.RerunMaxPerCandidate, err = envInt("CISYNC_CTRL_RERUN_MAX_PER_CANDIDATE", 2); err != nil {
		return nil, err
	}
	if cfg.VerifyInterval, err = envDuration("CISYNC_CTRL_VERIFY_INTERVAL", 0); err != nil {
		return nil, err
	}
	if cfg.AuditRetentionDays, err = envInt("CISYNC_CTRL_AUDIT_RETENTION_DAYS", 90); err != nil {
		return nil, err
	}
	if cfg.AuditRetentionDays < 90 {
		return nil, fmt.Errorf("config CISYNC_CTRL_AUDIT_RETENTION_DAYS: %d below the B7 90-day floor", cfg.AuditRetentionDays)
	}
	cfg.TrackedBaseBranches = parseList(env("CISYNC_CTRL_TRACKED_BASE_BRANCHES", "main,master"))
	if len(cfg.TrackedBaseBranches) == 0 {
		return nil, fmt.Errorf("config CISYNC_CTRL_TRACKED_BASE_BRANCHES: must not be empty")
	}
	if cfg.RepoBundlesDir = env("CISYNC_CTRL_REPO_BUNDLES_DIR", ""); cfg.RepoBundlesDir != "" {
		appIDStr := env("CISYNC_CTRL_GITHUB_APP_ID", "")
		keyFile := env("CISYNC_CTRL_GITHUB_APP_KEY_FILE", "")
		instIDStr := env("CISYNC_CTRL_GITHUB_INSTALLATION_ID", "")
		cfg.GitHubStaticToken = env("CISYNC_CTRL_GITHUB_TOKEN", "")
		if appIDStr != "" {
			id, err := strconv.ParseInt(appIDStr, 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("config CISYNC_CTRL_GITHUB_APP_ID %q: invalid", appIDStr)
			}
			cfg.GitHubAppID = id
		}
		if instIDStr != "" {
			id, err := strconv.ParseInt(instIDStr, 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("config CISYNC_CTRL_GITHUB_INSTALLATION_ID %q: invalid", instIDStr)
			}
			cfg.GitHubInstallationID = id
		}
		switch {
		case cfg.GitHubAppID > 0 && keyFile != "" && cfg.GitHubInstallationID > 0 && cfg.GitHubStaticToken == "":
			cfg.GitHubAppKeyFile = keyFile
		case cfg.GitHubAppID == 0 && keyFile == "" && cfg.GitHubInstallationID == 0 && cfg.GitHubStaticToken != "":
		default:
			// All-or-nothing mirrors ghauth's G14 posture: half-configured
			// credentials must refuse boot rather than mint unscoped tokens.
			return nil, fmt.Errorf(
				"config CISYNC_CTRL_REPO_BUNDLES_DIR: set EITHER GitHub App trio (APP_ID+APP_KEY_FILE+INSTALLATION_ID) OR CISYNC_CTRL_GITHUB_TOKEN")
		}
	}
	if cfg.AdminToken == "" {
		return nil, fmt.Errorf("config CISYNC_CTRL_ADMIN_TOKEN: required")
	}
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("config CISYNC_CTRL_WEBHOOK_SECRET: required")
	}
	return cfg, nil
}
