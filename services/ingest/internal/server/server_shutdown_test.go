package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"cisync.dev/cisync/ingest/internal/config"
	"cisync.dev/cisync/ingest/internal/obs"
	"cisync.dev/cisync/ingest/internal/store"
)

// freePort reserves an ephemeral port and releases it so the server under
// test can bind a KNOWN address (http.Server hides its resolved :0 port).
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// TestRunGracefulShutdownOnSIGTERMContext pins the H4 contract for ingest's
// lifecycle: Run blocks until ctx is cancelled (SIGTERM via
// signal.NotifyContext in main), drains in-flight work, returns nil so main
// exits 0, and the listener stops accepting.
func TestRunGracefulShutdownOnSIGTERMContext(t *testing.T) {
	addr := freePort(t)
	cfg := config.Config{
		Addr:           addr,
		SeenWindowTTL:  time.Hour,
		SeenMaxEntries: 16,
		RetryInterval:  time.Hour,
	}
	st := store.NewMemoryStore(nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg, st, logger, obs.New())

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	healthz := "http://" + addr + "/healthz"
	deadline := time.Now().Add(5 * time.Second)
	serving := false
	for !serving {
		if time.Now().After(deadline) {
			t.Fatal("server never started serving /healthz")
		}
		resp, err := http.Get(healthz)
		if err == nil {
			resp.Body.Close()
			serving = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("graceful shutdown must return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}

	// The endpoint must stop answering once shutdown completed.
	shutdownDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(shutdownDeadline) {
		resp, err := http.Get(healthz)
		if err != nil {
			return // connection refused: listener closed as required
		}
		resp.Body.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("healthz still reachable after graceful shutdown")
}
