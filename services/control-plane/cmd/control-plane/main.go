// Command control-plane is the CISync control-plane entrypoint: REST API,
// outbox relay, scheduler tick and reconciler. The `verify` subcommand runs
// the hash-chain verifier and exits non-zero on any mismatch.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/api"
	"cisync.dev/cisync/control-plane/internal/config"
	"cisync.dev/cisync/control-plane/internal/joblease"
	"cisync.dev/cisync/control-plane/internal/redact"
	"cisync.dev/cisync/control-plane/internal/relay"
	"cisync.dev/cisync/control-plane/internal/scheduler"
	"cisync.dev/cisync/control-plane/internal/store"
	"cisync.dev/cisync/control-plane/internal/verify"
)

func main() {
	// B3: every log line passes the fail-closed secret scrubber before it
	// reaches stdout/stderr sinks.
	logger := slog.New(slog.NewTextHandler(&redact.Writer{Next: os.Stderr}, nil))
	slog.SetDefault(logger)

	if len(os.Args) > 1 && os.Args[1] == "verify" {
		runVerify()
		return
	}
	runServer()
}

func loadStore(ctx context.Context, withKey bool) (*store.Store, *config.Config, func()) {
	cfg, err := config.Load()
	must(err, "load config")
	st, err := store.Open(ctx, cfg.PGDSN)
	must(err, "open store")
	if err := st.Migrate(ctx, migrationsDir()); err != nil {
		st.Close()
		must(err, "migrate")
	}
	if withKey && cfg.LedgerKeyFile != "" {
		err = st.UseSigningKey(cfg.LedgerKeyFile)
		if err != nil {
			st.Close()
			must(err, "load ledger key")
		}
	}
	return st, cfg, func() { st.Close() }
}

// migrationsDir resolves the migration folder relative to the service root.
func migrationsDir() string {
	for _, dir := range []string{"migrations", "services/control-plane/migrations"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return "migrations"
}

func runServer() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, cfg, closeStore := loadStore(ctx, true)

	srv := api.NewServer(cfg, st, nil)
	fleet := relay.NewFleetClient(cfg.FleetURL)

	// Metric parity: store-side same-tx audit inserts (teardown revocations)
	// must surface on the SAME counter as streamed emissions.
	securityAuditCounter := func(kind string) {
		srv.Metrics().Add("cisync_security_audit_total", 1, "kind", kind)
	}
	st.AuditObserver = securityAuditCounter

	// Job-lease minting is REQUIRED: the fleet rejects unauthenticated
	// mutations (B2/I-04), so dispatch without a signer would strand runs.
	if cfg.JobLeaseKeyFile == "" {
		must(errJobLeaseKeyMissing, "load job-lease key")
	}
	leaseSigner, err := joblease.NewSignerFromPEMFile(cfg.JobLeaseKeyFile)
	must(err, "load job-lease key")

	relayCtx, cancelRelay := context.WithCancel(ctx)
	defer cancelRelay()

	// workers tracks every background loop so graceful shutdown can WAIT
	// for them to finish BEFORE closing the PG pool they write through.
	var workers sync.WaitGroup

	outbox := relay.New(st, cfg.RelayBatchSize, cfg.RelayPollInterval)

	// Real engine scheduler: priority ranking + policy-capped admission +
	// fleet dispatch + completion ingestion (evidence/failure/decisions).
	engineScheduler := scheduler.NewEngine(st, fleet, "sim", cfg.SchedBatch, leaseSigner)
	engineScheduler.SetAuditObserver(securityAuditCounter)

	outbox.Register("validation.requested", func(ctx context.Context, item store.OutboxItem) error {
		return st.ExecTx(ctx, func(tx pgx.Tx) error {
			_, err := store.MarkProcessedTx(ctx, tx, "scheduler", item.EventID)
			return err
		})
	})
	if connectorURL := envOr("CISYNC_CTRL_CONNECTOR_URL", ""); connectorURL != "" {
		publisher := relay.NewConnectorPublisher(st, connectorURL,
			cfg.WebhookSecret, envOr("CISYNC_CTRL_DETAILS_URL", "http://localhost:3000"))
		outbox.Register("decision.rendered", publisher.ConsumeRendered)
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		outbox.Run(relayCtx)
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		engineScheduler.Run(relayCtx, cfg.TickInterval)
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		relay.NewReconciler(st, fleet, cfg.StaleRunMaxAge, cfg.AuditRetentionDays).
			Run(relayCtx, cfg.ReconcileInterval)
	}()

	// H3: in-process nightly chain verification (CISYNC_CTRL_VERIFY_INTERVAL;
	// 0 = off, prod 24h). Same verifier as the `verify` subcommand; failures
	// log structured, bump a metric, and land a security_audit row — but do
	// NOT halt serving (see verify.Scheduler WHY comment).
	if cfg.VerifyInterval > 0 {
		signer := resolveLedgerSigner(cfg)
		verifier := verify.New(st, signer)
		sched := verify.NewScheduler(verify.FromVerifier(verifier), cfg.VerifyInterval,
			st, cfg.TenantID,
			func(status string) {
				srv.Metrics().Add("cisync_ledger_verify_result", 1, "status", status)
			},
			securityAuditCounter)
		workers.Add(1)
		go func() {
			defer workers.Done()
			sched.Run(relayCtx)
		}()
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		fmt.Printf("control-plane listening on %s\n", cfg.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			must(err, "http serve")
		}
	}()

	<-ctx.Done()
	// Graceful shutdown order (H4): stop accepting connections and drain
	// in-flight requests (10s budget) → cancel background loops → WAIT for
	// them → flush the audit stream → only then close the PG pool, so no
	// worker writes into a closed pool.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http drain incomplete at shutdown", slog.String("err", err.Error()))
	}
	cancelRelay()
	workers.Wait()
	srv.StopAudit()
	closeStore()
}

// resolveLedgerSigner loads the checkpoint signing key when configured;
// without one the verifier still checks chain structure (dev posture).
func resolveLedgerSigner(cfg *config.Config) *store.Signer {
	if cfg.LedgerKeyFile == "" {
		return nil
	}
	sig, err := store.LoadSigningKey(cfg.LedgerKeyFile)
	must(err, "load ledger key")
	return sig
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func runVerify() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	st, cfg, cleanup := loadStore(ctx, true)
	defer cleanup()
	var signer *store.Signer
	if cfg.LedgerKeyFile != "" {
		sig, err := store.LoadSigningKey(cfg.LedgerKeyFile)
		must(err, "load ledger key")
		signer = sig
	}
	rep, err := verify.New(st, signer).Verify(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "VERIFY FAILED (fail-closed): %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("chain OK: %d entries, %d checkpoints verified\n", rep.Entries, rep.Checkpoints)
}

var errJobLeaseKeyMissing = fmt.Errorf("CISYNC_CTRL_JOBLEASE_KEY_FILE is required")

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "control-plane: %s: %v\n", what, err)
		os.Exit(1)
	}
}
