package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
	fstore "sauron.dev/sauron/runner-fleet/internal/store"
)

// P0 regression (live W4): external claims arrive with a worker_id that has
// no fleet.workers row ("anonymous" default). execution_jobs.claimed_by's FK
// rejected the claim UPDATE, so every probe/external claim 500'd while the
// internal executor (self-registered slots) kept working. The claim handler
// must register the claiming worker BEFORE claiming — outside the claim tx,
// which must never touch fleet.workers (W3 lock-convoy finding).

// recordingStore delegates to MemoryStore but enforces the pg registration
// contract: ClaimJobs fails unless EnsureWorker registered the worker first.
type recordingStore struct {
	*fstore.MemoryStore
	registered map[string]bool
}

func newRecordingStore(now func() time.Time) *recordingStore {
	return &recordingStore{MemoryStore: fstore.NewMemoryStore(now), registered: map[string]bool{}}
}

func (r *recordingStore) EnsureWorker(ctx context.Context, id, pool string, capacity int, now time.Time) error {
	r.registered[id] = true
	return r.MemoryStore.EnsureWorker(ctx, id, pool, capacity, now)
}

func (r *recordingStore) ClaimJobs(ctx context.Context, c fstore.Claim, now time.Time) ([]domain.Job, error) {
	if !r.registered[c.WorkerID] {
		return nil, errors.New("fk violation: unregistered claiming worker")
	}
	return r.MemoryStore.ClaimJobs(ctx, c, now)
}

func postClaim(t *testing.T, h http.Handler, body map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal claim body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/fleet/jobs/claim", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var bodyMap map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &bodyMap)
	return rec.Result(), bodyMap
}

func TestClaimRegistersUnknownWorkerBeforeClaiming(t *testing.T) {
	st := newRecordingStore(time.Now)
	handler := NewClaimHandler(st, slog.New(slog.NewTextHandler(os.Stderr, nil)), nil)

	if err := st.Enqueue(context.Background(), domain.Job{
		RunID: "run_01JCLAIMREG00000000000000", Attempt: 1, Tier: 1, Pool: "sim",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	resp, body := postClaim(t, handler, map[string]any{"pool": "sim", "limit": 4})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim with never-seen worker_id must register it and succeed, got %d: %v", resp.StatusCode, body)
	}
	jobs, _ := body["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("claim must return the queued job, got %v", body)
	}
}

// PG regression twin against the real schema (skips without TEST_PG_DSN):
// the FK on execution_jobs.claimed_by is what actually rejected the claim.
func TestClaimWithUnregisteredWorkerSucceedsAgainstPG(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping runner-fleet PG test")
	}
	ctx := context.Background()
	st, err := fstore.NewPGStore(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	runID := "run_01JCLAIMPG" + time.Now().UTC().Format("150405.000000000")
	if err := st.Enqueue(ctx, domain.Job{
		RunID: runID, Attempt: 1, Tier: 1, Pool: "test-claim-pg",
		Spec: domain.JobSpec{Kind: "selected_unit", Repo: "acme/claim-pg",
			BaseSHA: "1111111111111111111111111111111111111111",
			HeadSHA: "2222222222222222222222222222222222222222"},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	handler := NewClaimHandler(st, slog.New(slog.NewTextHandler(os.Stderr, nil)), nil)
	resp, body := postClaim(t, handler, map[string]any{"pool": "test-claim-pg", "limit": 4})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("external claim of an anonymous worker hit the store: %d %v", resp.StatusCode, body)
	}
	jobs, _ := body["jobs"].([]any)
	if len(jobs) == 0 {
		t.Fatalf("claim must return the enqueued job, got %v", body)
	}
}
