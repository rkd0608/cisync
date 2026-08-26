package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/config"
	"cisync.dev/cisync/control-plane/internal/domain"
	"cisync.dev/cisync/control-plane/internal/relay"
	"cisync.dev/cisync/control-plane/internal/store"
)

/**
 * PG-backed regression: out-of-order completion ingestion must never mutate
 * a decided (terminal) candidate and must never render eligible over an
 * unresolved required-kind failure. Fixtures: completions_pg_fixtures_test.go.
 */

func completionFor(runID string, fence int64, attempt int, status string) relay.CompletedJob {
	// P0-2: succeeded completions carry an honest full-pass census — the
	// scheduler fail-closes zero-executed outcomes to non-evidence.
	results := &relay.CompletedResults{Total: 8, Passed: 8}
	if status != "succeeded" {
		results = &relay.CompletedResults{Total: 6, Failed: 6}
	}
	return relay.CompletedJob{
		RunID: runID, Attempt: attempt, FenceToken: fence, Tier: 1, Pool: "sim",
		Status: status, LogsDigest: "sha256:" + fmt.Sprintf("%064d", 4),
		ArtifactDigests: []string{"sha256:" + fmt.Sprintf("%064d", 5)},
		DurationMS:      10, CostMillicents: 1, Results: results,
	}
}

func ledgerTypesAfter(t *testing.T, st *store.Store, afterSeq int64) map[string]int {
	t.Helper()
	rows, err := st.Pool.Query(context.Background(),
		`SELECT type FROM ctrl.ledger WHERE seq > $1`, afterSeq)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			t.Fatalf("scan ledger type: %v", err)
		}
		counts[typ]++
	}
	return counts
}

func TestLateCompletionOnDecidedCandidateIsAbsorbed(t *testing.T) {
	engine, st, done := pgScheduler(t)
	defer done()
	ctx := context.Background()
	tenantID := config.DevTenant
	tag := fmt.Sprintf("late-%d", time.Now().UnixNano())
	seeded := seedValidationCandidate(t, st, tenantID, tag)
	dispatchRuns(t, engine, seeded.runIDs)

	// First required-kind run succeeds → evidence recorded, no decision yet.
	engine.fleet = &fakeFleet{completed: []relay.CompletedJob{
		completionFor(seeded.runIDs[0], 1, 1, "succeeded"),
	}}
	consumed, err := engine.IngestCompletions(ctx)
	if err != nil || consumed != 1 {
		t.Fatalf("ingest first completion: consumed=%d err=%v", consumed, err)
	}

	// Force the decision so the candidate becomes terminal.
	err = st.ExecTx(ctx, func(tx pgx.Tx) error {
		return engine.renderDecision(ctx, tx, renderRequest{
			TenantID: tenantID, CandidateID: seeded.candidateID,
			Verb: domain.VerbEligibleForMergeTrain, Policy: domain.DefaultPolicy().Ref,
		})
	})
	if err != nil {
		t.Fatalf("render decision: %v", err)
	}
	headBefore, err := st.MaxSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The late completion of the second run lands AFTER the decision.
	engine.fleet = &fakeFleet{completed: []relay.CompletedJob{
		completionFor(seeded.runIDs[1], 1, 1, "succeeded"),
	}}
	consumed, err = engine.IngestCompletions(ctx)
	if err != nil {
		t.Fatalf("ingest late completion: %v", err)
	}
	if consumed != 0 {
		t.Fatalf("late completion must be absorbed as diagnostic, consumed=%d", consumed)
	}
	after := ledgerTypesAfter(t, st, headBefore)
	for _, forbidden := range []string{"validation.completed", "evidence.recorded"} {
		if after[forbidden] > 0 {
			t.Fatalf("post-decision %s was appended (I-08 violation): %v", forbidden, after)
		}
	}
}

func TestEligibleNeverRenderedOverUnresolvedRequiredFailure(t *testing.T) {
	engine, st, done := pgScheduler(t)
	defer done()
	ctx := context.Background()
	tenantID := config.DevTenant
	tag := fmt.Sprintf("failguard-%d", time.Now().UnixNano())
	seeded := seedValidationCandidate(t, st, tenantID, tag)
	dispatchRuns(t, engine, seeded.runIDs)

	// Required-kind run fails permanently at max retry → routed repair; no
	// decision may exist afterwards.
	engine.maxRetry = 1
	engine.fleet = &fakeFleet{completed: []relay.CompletedJob{
		completionFor(seeded.runIDs[0], 1, 1, "failed"),
	}}
	if _, err := engine.IngestCompletions(ctx); err != nil {
		t.Fatalf("ingest failed completion: %v", err)
	}
	if counts := ledgerTypesAfter(t, st, 0); counts["failure.classified"] == 0 {
		t.Fatal("setup: failed completion produced no failure.classified; regression test is vacuous")
	}

	// The sibling run then succeeds and would complete sufficiency — the
	// outstanding failure must keep the verb away from eligible.
	engine.fleet = &fakeFleet{completed: []relay.CompletedJob{
		completionFor(seeded.runIDs[1], 1, 1, "succeeded"),
	}}
	if _, err := engine.IngestCompletions(ctx); err != nil {
		t.Fatalf("ingest sibling success: %v", err)
	}
	if counts := ledgerTypesAfter(t, st, 0); counts["evidence.recorded"] == 0 {
		t.Fatal("setup: sibling success produced no accepted evidence; regression test is vacuous")
	}

	rows, err := st.Pool.Query(ctx,
		`SELECT payload::jsonb->'subject'->>'id', payload::jsonb->>'verb'
		 FROM ctrl.ledger WHERE type='decision.rendered'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var subject, verb string
		if err := rows.Scan(&subject, &verb); err != nil {
			t.Fatal(err)
		}
		if subject == seeded.candidateID && verb == string(domain.VerbEligibleForMergeTrain) {
			t.Fatalf("eligible rendered over unresolved required-kind failure for %s", seeded.candidateID)
		}
	}
}

func TestDecisionRendersWithThirdRequiredCompletion(t *testing.T) {
	engine, st, done := pgScheduler(t)
	defer done()
	ctx := context.Background()
	tenantID := config.DevTenant
	tag := fmt.Sprintf("suff-%d", time.Now().UnixNano())
	seeded := seedValidationCandidate(t, st, tenantID, tag)
	dispatchRuns(t, engine, seeded.runIDs)

	// Two required kinds succeed; neither may render a decision.
	for i := 0; i < 2; i++ {
		engine.fleet = &fakeFleet{completed: []relay.CompletedJob{
			completionFor(seeded.runIDs[i], 1, 1, "succeeded"),
		}}
		if _, err := engine.IngestCompletions(ctx); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}

	// The THIRD required-kind completion completes sufficiency INSIDE its own
	// effect tx: the decision must render in the same pass, without waiting
	// for any further completion to re-check (pool-vs-tx visibility).
	engine.fleet = &fakeFleet{completed: []relay.CompletedJob{
		completionFor(seeded.runIDs[2], 1, 1, "succeeded"),
	}}
	if _, err := engine.IngestCompletions(ctx); err != nil {
		t.Fatalf("ingest third: %v", err)
	}

	var count int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM ctrl.decisions WHERE tenant_id=$1 AND subject_id=$2`,
		tenantID, seeded.candidateID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("decision did not render with the sufficing completion (rows=%d)", count)
	}
}
