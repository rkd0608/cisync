// Package server tests exercise the full worker protocol over httptest with
// a hermetic memory store and injectable clock.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/config"
	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/joblease"
	"cisync.dev/cisync/runner-fleet/internal/obs"
	fstore "cisync.dev/cisync/runner-fleet/internal/store"
)

// harness wires the production mux against a memory store and a fixed clock.
// It also holds the job-lease test signer: every enqueued job is dispatched
// with a real credential so protocol tests exercise the same authenticated
// surface production runners use (THREAT_MODEL B2).
type harness struct {
	t       *testing.T
	svc     *httptest.Server
	srv     *Server
	st      *fstore.MemoryStore
	now     time.Time
	mu      sync.Mutex
	cancels []string
	signer  *joblease.Signer
	rogue   *joblease.Signer
	tokens  map[string]string // run_id -> presented credential
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC), tokens: map[string]string{}}
	signer, err := joblease.NewSignerForTesting()
	if err != nil {
		t.Fatalf("job lease test signer: %v", err)
	}
	rogue, err := joblease.NewSignerForTesting()
	if err != nil {
		t.Fatalf("rogue signer: %v", err)
	}
	h.signer = signer
	h.rogue = rogue
	verifier, err := joblease.NewVerifierFromPublicPEM(signer.PublicPEM())
	if err != nil {
		t.Fatalf("verifier wiring: %v", err)
	}
	verifier.Now = func() time.Time { return h.clock() }
	h.st = fstore.NewMemoryStore(func() time.Time { return h.clock() })
	cfg := config.Config{
		Pool:              "sim",
		Provider:          "sim",
		SimWorkers:        2,
		HeartbeatInterval: 5 * time.Second,
		PollInterval:      10 * time.Millisecond,
		GaugeInterval:     time.Hour,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg, h.st, h, logger, obs.New(), func() time.Time { return h.clock() }, verifier)
	h.srv = srv
	h.svc = httptest.NewServer(srv.Mux)
	t.Cleanup(h.svc.Close)
	return h
}

// Submit implements domain.Provider minimally for protocol tests; execution
// itself is driven by dedicated executor tests with scripted providers.
func (h *harness) Submit(_ context.Context, job domain.Job) (domain.Handle, error) {
	return job.RunID, nil
}

// Cancel implements domain.Provider and records cancellations.
func (h *harness) Cancel(handle domain.Handle) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cancels = append(h.cancels, handle.(string))
	return nil
}

// Poll implements domain.Provider as permanently running (never used here).
func (h *harness) Poll(domain.Handle) (domain.PollState, domain.Outcome) {
	return domain.PollRunning, domain.Outcome{}
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *harness) cancelledRuns() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.cancels...)
}

func (h *harness) post(path string, body any) (*http.Response, map[string]any) {
	return h.postWithAuth(path, "", body)
}

func (h *harness) complete(runID string, fence int64, status string) (*http.Response, map[string]any) {
	h.t.Helper()
	return h.completeWithToken(runID, fence, status, h.tokenFor(runID))
}

func (h *harness) heartbeat(runID string, fence int64) (*http.Response, map[string]any) {
	return h.heartbeatWithToken(runID, fence, h.tokenFor(runID))
}

func (h *harness) cancel(runID, reason string) (*http.Response, map[string]any) {
	return h.cancelWithToken(runID, reason, h.tokenFor(runID))
}

func digestFor(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
