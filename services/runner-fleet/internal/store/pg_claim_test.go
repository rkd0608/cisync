package store

import (
	"context"
	"os"
	"testing"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
)

// Live W4 regression: RequeueStale purges fleet.workers rows whose
// last_heartbeat ages past the threshold, but executor slots register once at
// startup and never refresh — the GC reaps LIVE slots mid-flight and every
// later claim dies on execution_jobs.claimed_by's FK (the 21:40 stall).
// ClaimJobs must therefore re-register an unknown claiming worker BEFORE its
// tx (outside it — claim transactions must never touch fleet.workers, W3
// lock-convoy finding), mirroring MemoryStore.ClaimJobs auto-registration.
// Skips without TEST_PG_DSN so hermetic runs stay green.
func TestPGClaimJobsSelfHealsUnknownWorker(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping runner-fleet PG test")
	}
	ctx := context.Background()
	st, err := NewPGStore(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	runID := "run_01JSELFHEAL" + time.Now().UTC().Format("150405.000000000")
	pool := "test-self-heal-" + time.Now().UTC().Format("150405.000000000")
	if err := st.Enqueue(ctx, domain.Job{
		RunID: runID, Attempt: 1, Tier: 1, Pool: pool,
		Spec: domain.JobSpec{Kind: "selected_unit", Repo: "acme/self-heal",
			BaseSHA: "1111111111111111111111111111111111111111",
			HeadSHA: "2222222222222222222222222222222222222222"},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The purged-slot shape: no EnsureWorker call ever happened for this id.
	claimed, err := st.ClaimJobs(ctx, Claim{
		Pool: pool, Provider: "external", Limit: 1,
		WorkerID: "worker_sim_purged_slot0",
	}, time.Now())
	if err != nil {
		t.Fatalf("claim of an unknown/purged worker must self-heal, got: %v", err)
	}
	if len(claimed) != 1 || claimed[0].RunID != runID {
		t.Fatalf("claim must return the queued job, got %+v", claimed)
	}
	if claimed[0].FenceToken < 1 {
		t.Fatalf("fence must bump on claim: %+v", claimed[0])
	}
}
