package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/relay"
)

/**
 * P0-2 / I-01 attack-vector regressions (PG-backed): a runner reporting an
 * all-skipped outcome with status=succeeded can NEVER mint pass evidence,
 * and partial skips are recorded as non-evidence metadata. Skips without
 * TEST_PG_DSN so hermetic runs stay green.
 */

func completionWithCensus(runID string, fence int64, attempt int, status string, total, passed, failed, skipped, quarantined int) relay.CompletedJob {
	job := completionFor(runID, fence, attempt, status)
	job.Results = &relay.CompletedResults{
		Total: total, Passed: passed, Failed: failed, Skipped: skipped, Quarantined: quarantined,
	}
	return job
}

func TestAllSkippedSucceededMintsNoPassEvidence(t *testing.T) {
	engine, st, done := pgScheduler(t)
	defer done()
	ctx := context.Background()
	tenantID := config.DevTenant
	tag := fmt.Sprintf("skipvec-%d", time.Now().UnixNano())
	seeded := seedValidationCandidate(t, st, tenantID, tag)
	dispatchRuns(t, engine, seeded.runIDs)

	engine.fleet = &fakeFleet{completed: []relay.CompletedJob{
		completionWithCensus(seeded.runIDs[0], 1, 1, "succeeded", 10, 0, 0, 10, 0),
	}}
	if _, err := engine.IngestCompletions(ctx); err != nil {
		t.Fatalf("ingest all-skips completion: %v", err)
	}

	var evCount int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM ctrl.evidence_records WHERE tenant_id=$1 AND run_id=$2`,
		tenantID, seeded.runIDs[0]).Scan(&evCount); err != nil {
		t.Fatal(err)
	}
	if evCount != 0 {
		t.Fatalf("all-skips+succeeded must mint ZERO accepted evidence, got %d records", evCount)
	}
	var decCount int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM ctrl.decisions WHERE tenant_id=$1 AND subject_id=$2`,
		tenantID, seeded.candidateID).Scan(&decCount); err != nil {
		t.Fatal(err)
	}
	if decCount != 0 {
		t.Fatalf("zero-executed runs cannot satisfy sufficiency; decision rendered (%d)", decCount)
	}
}

func TestPartiallySkippedAcceptsWithSkipMeta(t *testing.T) {
	engine, st, done := pgScheduler(t)
	defer done()
	ctx := context.Background()
	tenantID := config.DevTenant
	tag := fmt.Sprintf("partialskip-%d", time.Now().UnixNano())
	seeded := seedValidationCandidate(t, st, tenantID, tag)
	dispatchRuns(t, engine, seeded.runIDs)

	engine.fleet = &fakeFleet{completed: []relay.CompletedJob{
		completionWithCensus(seeded.runIDs[0], 1, 1, "succeeded", 10, 7, 0, 3, 0),
	}}
	consumed, err := engine.IngestCompletions(ctx)
	if err != nil || consumed != 1 {
		t.Fatalf("ingest partial-skip completion: consumed=%d err=%v", consumed, err)
	}

	var payload []byte
	if err := st.Pool.QueryRow(ctx,
		`SELECT payload FROM ctrl.ledger WHERE tenant_id=$1 AND type='evidence.recorded' ORDER BY seq DESC LIMIT 1`,
		tenantID).Scan(&payload); err != nil {
		t.Fatalf("accepted evidence.recorded missing: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	meta, ok := doc["outcome_meta"].(map[string]any)
	if !ok {
		t.Fatalf("accepted record must carry outcome_meta: %v", doc)
	}
	if meta["skipped_as_non_evidence"] != "3" {
		t.Fatalf("meta must record skipped_as_non_evidence=3, got %v", meta)
	}
}

func TestHonestFullPassStillAcceptedWithCensus(t *testing.T) {
	engine, st, done := pgScheduler(t)
	defer done()
	ctx := context.Background()
	tenantID := config.DevTenant
	tag := fmt.Sprintf("honestpass-%d", time.Now().UnixNano())
	seeded := seedValidationCandidate(t, st, tenantID, tag)
	dispatchRuns(t, engine, seeded.runIDs)

	engine.fleet = &fakeFleet{completed: []relay.CompletedJob{
		completionWithCensus(seeded.runIDs[0], 1, 1, "succeeded", 12, 12, 0, 0, 0),
	}}
	consumed, err := engine.IngestCompletions(ctx)
	if err != nil || consumed != 1 {
		t.Fatalf("ingest honest-pass completion: consumed=%d err=%v", consumed, err)
	}
	var evCount int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM ctrl.evidence_records WHERE tenant_id=$1 AND run_id=$2`,
		tenantID, seeded.runIDs[0]).Scan(&evCount); err != nil {
		t.Fatal(err)
	}
	if evCount != 1 {
		t.Fatalf("honest full-pass census must accept exactly one record, got %d", evCount)
	}
}
