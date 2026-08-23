// Command control-plane is the Sauron control-plane entrypoint: REST API,
// outbox relay, scheduler tick and reconciler. The `verify` subcommand runs
// the hash-chain verifier and exits non-zero on any mismatch.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sauron.dev/sauron/control-plane/internal/api"
	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
	"sauron.dev/sauron/control-plane/internal/verify"
)

func main() {
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

	st, cfg, cleanup := loadStore(ctx, true)
	defer cleanup()

	srv := api.NewServer(cfg, st, nil)
	fleet := relay.NewFleetClient(cfg.FleetURL)

	relayCtx, cancelRelay := context.WithCancel(ctx)
	defer cancelRelay()
	outbox := relay.New(st, cfg.RelayBatchSize, cfg.RelayPollInterval)
	dispatcher := relay.NewDispatcher(st, fleet, "sim", 8)
	outbox.Register("validation.requested", dispatcher.ConsumeRequested)
	go outbox.Run(relayCtx)
	go dispatcher.Run(relayCtx, cfg.TickInterval)
	go relay.NewReconciler(st, fleet).Run(relayCtx, cfg.ReconcileInterval)

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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
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

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "control-plane: %s: %v\n", what, err)
		os.Exit(1)
	}
}
