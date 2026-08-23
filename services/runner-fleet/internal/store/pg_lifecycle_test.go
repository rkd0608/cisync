package store

import (
	"context"
	"os"
	"testing"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
)

// openTestPG connects to TEST_PG_DSN when provided; otherwise the test skips
// so hermetic `go test ./...` runs stay green without docker.
func openTestPG(t *testing.T) *PGStore {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping PG-backed store test")
	}
	ctx := context.Background()
	st, err := NewPGStore(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	if err := st.Migrate(ctx, "../../migrations"); err != nil {
		st.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// TestClaimAutoRegistersUnknownWorker pins the external-claim protocol: a
// worker that never registered before (e.g. "anonymous" from an external
// probe) must be registered atomically with the claim — the
// execution_jobs.claimed_by FK makes an unregistered claim impossible, and it
// must surface as success, never a 500.
func TestClaimAutoRegistersUnknownWorker(t *testing.T) {
	st := openTestPG(t)
	ctx := context.Background()
	now := time.Now().UTC()

	const runID = "run_claim_anon_reg"
	if _, err := st.pool.Exec(ctx, `DELETE FROM fleet.execution_jobs WHERE run_id=$1`, runID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `DELETE FROM fleet.execution_jobs WHERE run_id=$1`, runID)
	})

	err := st.Enqueue(ctx, domain.Job{RunID: runID, Attempt: 1, Tier: 1, Pool: "sim"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	claimed, err := st.ClaimJobs(ctx, Claim{Pool: "sim", Provider: "external", Limit: 4, WorkerID: "anonymous"}, now)
	if err != nil {
		t.Fatalf("claim with unknown worker id must succeed: %v", err)
	}
	found := false
	for _, j := range claimed {
		if j.RunID == runID {
			found = true
			if j.FenceToken < 1 {
				t.Fatalf("fence must bump on claim: %+v", j)
			}
		}
	}
	if !found {
		t.Fatal("enqueued job must be claimed")
	}
	var registered bool
	if err := st.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM fleet.workers WHERE id='anonymous')`).Scan(&registered); err != nil {
		t.Fatalf("worker lookup: %v", err)
	}
	if !registered {
		t.Fatal("claiming worker must be registered in fleet.workers")
	}
}
