// Package server tests exercise the full worker protocol over httptest with
// a hermetic memory store and injectable clock.
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/config"
	"sauron.dev/sauron/runner-fleet/internal/domain"
	"sauron.dev/sauron/runner-fleet/internal/obs"
	fstore "sauron.dev/sauron/runner-fleet/internal/store"
)

// harness wires the production mux against a memory store and a fixed clock.
type harness struct {
	t       *testing.T
	svc     *httptest.Server
	srv     *Server
	st      *fstore.MemoryStore
	now     time.Time
	mu      sync.Mutex
	cancels []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
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
	srv := New(cfg, h.st, h, logger, obs.New(), func() time.Time { return h.clock() })
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
	h.t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(h.svc.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		h.t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil && resp.ContentLength != 0 {
		decoded = nil
	}
	return resp, decoded
}

type testJobParams struct {
	runID      string
	attempt    int
	tier       int
	durationMS int64
	bias       string
	timeoutMS  int64
}

func (h *harness) enqueue(p testJobParams) domain.Job {
	h.t.Helper()
	if p.runID == "" {
		p.runID = "run_01JTEST0000000000000000000"
	}
	if p.attempt == 0 {
		p.attempt = 1
	}
	if p.durationMS == 0 {
		p.durationMS = 50
	}
	if p.bias == "" {
		p.bias = "pass"
	}
	if p.timeoutMS == 0 {
		p.timeoutMS = 60000
	}
	job := domain.Job{
		RunID:   p.runID,
		Attempt: p.attempt,
		Tier:    p.tier,
		Pool:    "sim",
		Spec: domain.JobSpec{
			Kind:      "selected_unit",
			Repo:      "acme/payments",
			BaseSHA:   "1111111111111111111111111111111111111111",
			HeadSHA:   "2222222222222222222222222222222222222222",
			TimeoutMS: p.timeoutMS,
			SimProfile: &domain.SimProfile{
				DurationMS:  p.durationMS,
				OutcomeBias: p.bias,
			},
		},
	}
	if err := h.st.Enqueue(context.Background(), job); err != nil {
		h.t.Fatalf("enqueue %s: %v", p.runID, err)
	}
	return job
}

type claimedJobView struct {
	RunID      string
	FenceToken int64
	Attempt    int
	Tier       int
	Pool       string
	Spec       domain.JobSpec
}

func (h *harness) claim(workerID string, limit int) []claimedJobView {
	h.t.Helper()
	resp, body := h.post("/internal/fleet/jobs/claim", map[string]any{"pool": "sim", "limit": limit, "worker_id": workerID})
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("claim must 200, got %d (%v)", resp.StatusCode, body)
	}
	jobsAny, _ := body["jobs"].([]any)
	var out []claimedJobView
	for _, j := range jobsAny {
		m := j.(map[string]any)
		view := claimedJobView{RunID: m["run_id"].(string), FenceToken: int64(m["fence_token"].(float64))}
		if v, ok := m["attempt"].(float64); ok {
			view.Attempt = int(v)
		}
		if v, ok := m["tier"].(float64); ok {
			view.Tier = int(v)
		}
		if v, ok := m["pool"].(string); ok {
			view.Pool = v
		}
		out = append(out, view)
	}
	return out
}

func (h *harness) complete(runID string, fence int64, status string) (*http.Response, map[string]any) {
	h.t.Helper()
	return h.post("/internal/fleet/jobs/"+runID+"/complete", map[string]any{
		"fence_token":            fence,
		"status":                 status,
		"logs_digest":            digestFor([]byte("logs-" + runID)),
		"artifact_digests":       []string{digestFor([]byte("art-" + runID))},
		"duration_ms":            42000,
		"actual_cost_millicents": 180,
	})
}

func (h *harness) heartbeat(runID string, fence int64) (*http.Response, map[string]any) {
	h.t.Helper()
	return h.post("/internal/fleet/jobs/"+runID+"/heartbeat", map[string]any{"fence_token": fence})
}

func (h *harness) cancel(runID, reason string) (*http.Response, map[string]any) {
	h.t.Helper()
	return h.post("/internal/fleet/jobs/"+runID+"/cancel", map[string]any{"reason": reason})
}

func digestFor(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
