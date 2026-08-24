package scheduler

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
)

/**
 * Shared PG-backed fixtures for the decision-freshness regressions: a fake
 * fleet gateway plus a deterministic intent→candidate→plan→runs seeder.
 * Skips without TEST_PG_DSN so hermetic runs stay green.
 */

type fakeFleet struct {
	completed []relay.CompletedJob
	cancelled []string
	enqueued  []relay.EnqueueRequest
}

func (f *fakeFleet) Enqueue(_ context.Context, req relay.EnqueueRequest) error {
	f.enqueued = append(f.enqueued, req)
	return nil
}
func (f *fakeFleet) Completed(_ context.Context, _ int) ([]relay.CompletedJob, error) {
	return f.completed, nil
}
func (f *fakeFleet) Cancel(_ context.Context, runID, _ string) error {
	f.cancelled = append(f.cancelled, runID)
	return nil
}

func pgScheduler(t *testing.T) (*EngineScheduler, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping scheduler PG test")
	}
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	if err := st.Migrate(context.Background(), "../../migrations"); err != nil {
		st.Close()
		t.Fatalf("migrate: %v", err)
	}
	fleet := &fakeFleet{}
	engine := NewEngine(st, fleet, "sim", 8, nil)
	return engine, st, func() { st.Close() }
}

type seededCandidate struct {
	intentID    string
	candidateID string
	runIDs      []string
}

// seedValidationCandidate inserts intent + lease + candidate + plan + two
// runs whose job kinds are exactly the plan's required evidence kinds.
func seedValidationCandidate(t *testing.T, st *store.Store, tenantID, tag string) seededCandidate {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	intentID := domain.NewID(domain.PrefixIntent)
	candID := domain.NewID(domain.PrefixCandidate)
	declared := domain.IntentDeclared{
		Goal: "guard regression seed", Repo: fmt.Sprintf("acme/guard-%s", tag), BaseRef: "main",
		BaseSnapshot: "main@guard", OwnedSurfaces: []string{"services/**"},
		RiskClass:      domain.RiskMedium,
		Origin:         domain.OriginSynthetic,
		ResolvedPolicy: domain.DefaultPolicy().Ref,
		ComputeBudget:  domain.BudgetValues{CPUMinutes: 10, EnvironmentMinutes: 10, RepairAttempts: 1},
	}
	intent := domain.NewIntent(intentID, tenantID, declared, now)
	intent.InitialCandidateID = candID
	lease := domain.NewLease(domain.NewID(domain.PrefixLease), tenantID, intentID,
		domain.LeaseScope{Kind: domain.ScopeChangeScope, Surfaces: declared.OwnedSurfaces},
		"agent:"+tenantID, declared.ComputeBudget, time.Minute, []string{"hermetic_build"}, now)
	if err := st.ExecTx(ctx, func(tx pgx.Tx) error {
		_, err := store.CreateIntentTx(ctx, tx, st, intent, lease, nil)
		return err
	}); err != nil {
		t.Fatalf("seed intent: %v", err)
	}

	cand, err := domain.NewCandidate(candID, tenantID, intentID, "guard-test",
		fmt.Sprintf("bundle:%s", tag), fmt.Sprintf("%040d", 1), fmt.Sprintf("%040d", 2),
		[]string{"services/checkout/**"}, 10, now)
	if err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	// Matches the default pack's medium-risk required set so the seeded
	// candidate can actually reach sufficiency when all kinds complete.
	kinds := []string{"hermetic_build", "selected_unit", "api_compat"}
	plan := domain.NewValidationPlan(domain.NewID(domain.PrefixPlan), tenantID, candID,
		[]domain.Tier{{Tier: 1, Jobs: kinds, Rationale: "guard test"}}, kinds,
		domain.DefaultPolicy().Ref, "sha256:"+fmt.Sprintf("%064d", 3), now)
	var runs []*domain.ValidationRun
	for i, kind := range kinds {
		spec := domain.JobSpec{Kind: kind, Repo: declared.Repo, BaseSHA: cand.BaseSHA,
			HeadSHA: cand.HeadSHA, PatchRef: cand.PatchRef}
		runs = append(runs, domain.NewValidationRun(domain.NewID(domain.PrefixRun), tenantID,
			plan.ID, candID, 1, spec, "sim", 100, 1, float64(i), now))
	}
	if err := st.ExecTx(ctx, func(tx pgx.Tx) error {
		_, err := store.SubmitCandidateTx(ctx, tx, st, cand, plan, runs, nil)
		return err
	}); err != nil {
		t.Fatalf("seed candidate tx: %v", err)
	}
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return seededCandidate{intentID: intentID, candidateID: candID, runIDs: ids}
}

// dispatchRuns walks the seeded runs through queued→dispatched exactly like
// the scheduler does, so seeded completions carry the post-claim fence (I-11).
func dispatchRuns(t *testing.T, engine *EngineScheduler, runIDs []string) {
	t.Helper()
	resolved := DefaultPolicySource()
	for _, id := range runIDs {
		if _, err := engine.dispatchOne(context.Background(), id,
			BudgetReservation{}, resolved); err != nil {
			t.Fatalf("dispatch %s: %v", id, err)
		}
	}
}

var _ = config.DevTenant
