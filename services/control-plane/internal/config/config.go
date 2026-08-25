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
	TenantID        string

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

// Load parses SAURON_CTRL_* environment variables into Config; required vars
// error out when missing.
func Load() (*Config, error) {
	cfg := &Config{
		PGDSN:           env("SAURON_CTRL_PG_DSN", "postgres://sauron:sauron_dev_only@localhost:5432/sauron"),
		Addr:            env("SAURON_CTRL_ADDR", ":8081"),
		FleetURL:        env("SAURON_CTRL_FLEET_URL", "http://localhost:8082"),
		AdminToken:      env("SAURON_CTRL_ADMIN_TOKEN", ""),
		WebhookSecret:   env("SAURON_CTRL_WEBHOOK_SECRET", env("SAURON_INGEST_WEBHOOK_SECRET", "")),
		LedgerKeyFile:   env("SAURON_CTRL_LEDGER_KEY_FILE", ""),
		JobLeaseKeyFile: env("SAURON_CTRL_JOBLEASE_KEY_FILE", ""),
		TenantID:        env("SAURON_CTRL_TENANT_ID", DevTenant),
	}
	var err error
	if cfg.RelayBatchSize, err = envInt("SAURON_CTRL_RELAY_BATCH", 100); err != nil {
		return nil, err
	}
	if cfg.RelayPollInterval, err = envDuration("SAURON_CTRL_RELAY_POLL", 500*time.Millisecond); err != nil {
		return nil, err
	}
	if cfg.TickInterval, err = envDuration("SAURON_CTRL_TICK_INTERVAL", time.Second); err != nil {
		return nil, err
	}
	if cfg.ReconcileInterval, err = envDuration("SAURON_CTRL_RECONCILE_INTERVAL", 30*time.Second); err != nil {
		return nil, err
	}
	if cfg.StaleRunMaxAge, err = envDuration("SAURON_CTRL_STALE_RUN_MAX_AGE", 30*time.Minute); err != nil {
		return nil, err
	}
	if cfg.DefaultLeaseTTL, err = envDuration("SAURON_CTRL_LEASE_TTL", 1500*time.Second); err != nil {
		return nil, err
	}
	if cfg.RateLimitPerMin, err = envInt("SAURON_CTRL_RATE_LIMIT_PER_MIN", 120); err != nil {
		return nil, err
	}
	if cfg.SchedBatch, err = envInt("SAURON_CTRL_SCHED_BATCH", 8); err != nil {
		return nil, err
	}
	if cfg.RerunMaxPerCandidate, err = envInt("SAURON_CTRL_RERUN_MAX_PER_CANDIDATE", 2); err != nil {
		return nil, err
	}
	cfg.TrackedBaseBranches = parseList(env("SAURON_CTRL_TRACKED_BASE_BRANCHES", "main,master"))
	if len(cfg.TrackedBaseBranches) == 0 {
		return nil, fmt.Errorf("config SAURON_CTRL_TRACKED_BASE_BRANCHES: must not be empty")
	}
	if cfg.AdminToken == "" {
		return nil, fmt.Errorf("config SAURON_CTRL_ADMIN_TOKEN: required")
	}
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("config SAURON_CTRL_WEBHOOK_SECRET: required")
	}
	return cfg, nil
}
