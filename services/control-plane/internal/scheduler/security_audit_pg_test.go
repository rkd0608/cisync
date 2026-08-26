package scheduler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/audit"
	"sauron.dev/sauron/control-plane/internal/domain"
	evidencepkg "sauron.dev/sauron/control-plane/internal/evidence"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
)

// TestEvidenceTamperAuditHelperPersistsExactlyOnce pins the B7 tamper
// emission at the helper level: quarantine rulings persist exactly one
// security-audit row inside the caller tx; ordinary rejections persist none.
// (The natural completion-feed path cannot reach tamper rulings today because
// ExpectedDigests stays nil until artifact manifests land post-v1.)
func TestEvidenceTamperAuditHelperPersistsExactlyOnce(t *testing.T) {
	engine, st, done := pgScheduler(t)
	defer done()
	ctx := context.Background()

	run := &domain.ValidationRun{ID: "run_tamperprobe", TenantID: "org_saufence",
		CandidateID: "cand_tamperprobe"}
	before := countAuditRows(t, st, audit.KindEvidenceTamper)

	tampered := evidencepkg.Evaluation{Action: evidencepkg.ActionQuarantine,
		Reason: evidencepkg.ReasonDigestManifestMismatch}
	err := st.ExecTx(ctx, func(tx pgx.Tx) error {
		engine.auditEvidenceTamperTx(ctx, tx, run, "hermetic_build", tampered)
		return nil
	})
	if err != nil {
		t.Fatalf("tamper emission tx: %v", err)
	}
	if got := countAuditRows(t, st, audit.KindEvidenceTamper); got != before+1 {
		t.Fatalf("evidence_tamper rows delta = %d, want exactly 1", got-before)
	}

	// Ordinary validation rejection (I-01 class): must NOT audit.
	ordinary := evidencepkg.Evaluation{Action: evidencepkg.ActionReject,
		Reason: evidencepkg.ReasonSkipNeverPositive}
	err = st.ExecTx(ctx, func(tx pgx.Tx) error {
		engine.auditEvidenceTamperTx(ctx, tx, run, "hermetic_build", ordinary)
		return nil
	})
	if err != nil {
		t.Fatalf("ordinary rejection tx: %v", err)
	}
	if got := countAuditRows(t, st, audit.KindEvidenceTamper); got != before+1 {
		t.Fatalf("non-tamper rejection must not audit; delta now %d, want %d", got-before, 1)
	}
}

// countAuditRows returns ctrl.security_audit rows for one kind.
func countAuditRows(t *testing.T, st *store.Store, kind audit.Kind) int64 {
	t.Helper()
	var n int64
	err := st.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ctrl.security_audit WHERE event_kind=$1`, string(kind)).Scan(&n)
	if err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

// pgAuditEngine builds an engine whose fake fleet THIS test owns, so feed
// rows can be staged between ticks.
func pgAuditEngine(t *testing.T) (*EngineScheduler, *store.Store, *fakeFleet, func()) {
	t.Helper()
	engine, st, done := pgScheduler(t)
	return engine, st, engine.fleet.(*fakeFleet), done
}

// TestStaleFenceCompletionEmitsOneAuditRow pins the B7 fence-mismatch
// emission point: exactly ONE row per stale completion, and none on its
// replays (the diagnostic absorption path keeps re-presented rows silent).
func TestStaleFenceCompletionEmitsOneAuditRow(t *testing.T) {
	engine, st, fleet, done := pgAuditEngine(t)
	defer done()
	ctx := context.Background()

	seeded := seedValidationCandidate(t, st, "org_saufence", "fence")
	dispatchRuns(t, engine, seeded.runIDs[:1])

	before := countAuditRows(t, st, audit.KindFenceMismatch)

	// A completion presenting a STALE fence token (dispatch stamped 1).
	fleet.completed = []relay.CompletedJob{{
		RunID: seeded.runIDs[0], Attempt: 1, FenceToken: 99, Status: "succeeded",
		DurationMS: 1000,
	}}
	if _, err := engine.IngestCompletions(ctx); err != nil {
		t.Fatalf("ingest completions: %v", err)
	}
	if got := countAuditRows(t, st, audit.KindFenceMismatch); got != before+1 {
		t.Fatalf("fence_mismatch rows delta = %d, want 1", got-before)
	}
	// The stale row is NOT absorbed (no MarkProcessed on that path), but the
	// emission must stay exactly-once per stale completion — tick again.
	if _, err := engine.IngestCompletions(ctx); err != nil {
		t.Fatalf("re-ingest completions: %v", err)
	}
	if got := countAuditRows(t, st, audit.KindFenceMismatch); got != before+1 {
		t.Fatalf("fence_mismatch rows after replay = %d, want %d", got, before+1)
	}
}

// exhaustTenantCPU pushes the tenant's hourly cpu_minutes counter to the
// policy ceiling so admission denies with a BUDGET-class reason.
func exhaustTenantCPU(ctx context.Context, t *testing.T, st *store.Store, tenantID string) error {
	t.Helper()
	return st.ExecTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO ctrl.budget_counters (tenant_id, kind, used, updated_seq, window_started_at)
			 VALUES ($1,'cpu_minutes',5000,0,date_trunc('hour', now()))
			 ON CONFLICT (tenant_id, kind) DO UPDATE
			   SET used = 5000, window_started_at = date_trunc('hour', now())`,
			tenantID)
		return err
	})
}

// TestBudgetAdmissionDenialAuditsOncePerRun pins the B7 budget_exceeded
// admission emission: one row when a run is first denied by an exhausted
// budget, no flood on subsequent ticks.
func TestBudgetAdmissionDenialAuditsOncePerRun(t *testing.T) {
	engine, st, _, done := pgAuditEngine(t)
	defer done()
	ctx := context.Background()

	// WHY exhaust FIRST: the shared dev stack's own control-plane polls this
	// database and could dispatch freshly-seeded queued runs inside the
	// window before exhaustion lands.
	if err := exhaustTenantCPU(ctx, t, st, "org_saubudget"); err != nil {
		t.Fatalf("exhaust budget: %v", err)
	}
	seedValidationCandidate(t, st, "org_saubudget", "budget")

	before := countAuditRows(t, st, audit.KindBudgetExceeded)
	if _, err := engine.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := countAuditRows(t, st, audit.KindBudgetExceeded); got < before+1 {
		t.Fatalf("budget_exceeded rows delta = %d, want >= 1", got-before)
	}
	firstPass := countAuditRows(t, st, audit.KindBudgetExceeded)

	// Second tick with the run still queued and still denied: no duplicate
	// emission (dedupe is per run id, not per tick).
	if _, err := engine.Tick(ctx); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if got := countAuditRows(t, st, audit.KindBudgetExceeded); got != firstPass {
		t.Fatalf("budget_exceeded rows after second tick = %d, want %d (exactly-once per run)",
			got, firstPass)
	}
}
