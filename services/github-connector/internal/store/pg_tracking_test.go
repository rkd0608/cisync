package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/domain"
	"cisync.dev/cisync/github-connector/internal/tracking"
)

// Compile-time wiring proof: the PG-backed adapter satisfies the tracking
// seam the decisions handler consumes.
var _ tracking.Store = (*PGTracker)(nil)

func uniqueRevision(prefix string) (candidate, headSHA string) {
	nano := time.Now().UnixNano()
	return "cand_" + prefix + "_" + fmt.Sprintf("%020d", nano),
		fmt.Sprintf("%040x", nano)
}

func cachedDecisionEnvelope() *domain.DecisionEnvelope {
	return &domain.DecisionEnvelope{
		Kind: domain.KindDecision, DecisionID: "dec_pg_" + fmt.Sprintf("%016x", time.Now().UnixNano()),
		CandidateID: "cand_01JPGTEST", Repo: "acme/payments",
		HeadSHA: "cccccccccccccccccccccccccccccccccccccccc",
		Verb:    domain.VerbEligibleForMergeTrain, Confidence: 0.9,
		Policy:     domain.PolicyRef{PolicyID: "pol_cisync_default", Version: 1},
		RenderedAt: time.Now().UTC(),
		Evidence:   &domain.EvidenceCounts{Required: 4, Accepted: 4, Deferred: 0, Failed: 0},
	}
}

func TestPGTrackerLifecycleUpsertMerge(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	tr := NewTracker(st)
	cand, head := uniqueRevision("life")

	// Unknown revision ⇒ ErrNotFound (rerun handler's typed 404 path).
	if _, err := tr.LookupCheckReport(ctx, cand, head); !errors.Is(err, tracking.ErrNotFound) {
		t.Fatalf("unknown revision must be ErrNotFound, got %v", err)
	}

	// queued: lifecycle events arrive BEFORE any decision — no decision_id yet.
	require.NoError(t, tr.RecordCheckReport(ctx, tracking.Record{
		CandidateID: cand, HeadSHA: head, Repo: "acme/payments",
		CheckRunID: 71, Phase: domain.PhaseQueued,
	}))
	rec, err := tr.LookupCheckReport(ctx, cand, head)
	require.NoError(t, err)
	require.Equal(t, int64(71), rec.CheckRunID)
	require.Equal(t, domain.PhaseQueued, rec.Phase)
	require.Empty(t, rec.DecisionID)

	// Partial update: a phase flip must NOT blank the check_run_id.
	require.NoError(t, tr.RecordCheckReport(ctx, tracking.Record{
		CandidateID: cand, HeadSHA: head, Phase: domain.PhaseInProgress,
	}))
	rec, _ = tr.LookupCheckReport(ctx, cand, head)
	require.Equal(t, domain.PhaseInProgress, rec.Phase)
	require.Equal(t, int64(71), rec.CheckRunID, "partial updates keep tracked fields")

	// Decision applied at completion; FindByDecision must locate the revision
	// WITH its decoded payload (replay_cached source).
	decision := cachedDecisionEnvelope()
	require.NoError(t, tr.RecordCheckReport(ctx, tracking.Record{
		CandidateID: cand, HeadSHA: head, Phase: domain.PhaseCompleted,
		Conclusion: "success", DecisionID: decision.DecisionID, LastDecision: decision,
	}))
	rec, _ = tr.LookupCheckReport(ctx, cand, head)
	require.Equal(t, domain.PhaseCompleted, rec.Phase)
	require.Equal(t, "success", rec.Conclusion)

	byDecision, err := tr.FindByDecision(ctx, decision.DecisionID)
	require.NoError(t, err)
	require.Equal(t, cand, byDecision.CandidateID)
	require.NotNil(t, byDecision.LastDecision)
	require.Equal(t, decision.DecisionID, byDecision.LastDecision.DecisionID)

	// Unknown/stale decision ids never resolve.
	if _, err := tr.FindByDecision(ctx, "dec_never_seen"); !errors.Is(err, tracking.ErrNotFound) {
		t.Fatalf("stale decision lookup must be ErrNotFound, got %v", err)
	}
}

func TestPGTrackerOpenCheckReportsFeedsSweeper(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	tr := NewTracker(st)
	staleCand, staleHead := uniqueRevision("stale")
	freshCand, freshHead := uniqueRevision("fresh")

	require.NoError(t, tr.RecordCheckReport(ctx, tracking.Record{
		CandidateID: staleCand, HeadSHA: staleHead, Phase: domain.PhaseQueued,
	}))
	require.NoError(t, tr.RecordCheckReport(ctx, tracking.Record{
		CandidateID: freshCand, HeadSHA: freshHead, Phase: domain.PhaseQueued,
	}))

	// Backdate the stale revision past any realistic threshold (same package:
	// direct UPDATE mirrors what production clock passage would do).
	tag, err := st.pool.Exec(ctx,
		`UPDATE ghconn.revision_tracking SET updated_at = now() - interval '2 hours'
		 WHERE candidate_id=$1 AND head_sha=$2`, staleCand, staleHead)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())

	open, err := tr.OpenCheckReports(ctx, time.Now().Add(-time.Minute), 50)
	require.NoError(t, err)
	var foundStale, foundFresh bool
	for _, rec := range open {
		switch rec.CandidateID {
		case staleCand:
			foundStale = true
			require.Equal(t, domain.PhaseQueued, rec.Phase)
		case freshCand:
			foundFresh = true
		}
	}
	require.True(t, foundStale, "backdated non-completed revision must feed the sweeper")
	require.False(t, foundFresh, "fresh revision stays outside the threshold")

	// Completed revisions never re-enter the sweeper feed.
	require.NoError(t, tr.RecordCheckReport(ctx, tracking.Record{
		CandidateID: staleCand, HeadSHA: staleHead, Phase: domain.PhaseCompleted,
		Conclusion: "neutral",
	}))
	open, err = tr.OpenCheckReports(ctx, time.Now().Add(-time.Minute), 50)
	require.NoError(t, err)
	for _, rec := range open {
		require.NotEqual(t, staleCand, rec.CandidateID, "completed revisions leave the open set")
	}
}

// TestPGTrackerLastDecisionRoundTrip guards the jsonb codec: whatever the
// handler stores must decode byte-equivalently for replay_cached.
func TestPGTrackerLastDecisionRoundTrip(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	tr := NewTracker(st)
	cand, head := uniqueRevision("codec")
	decision := cachedDecisionEnvelope()

	require.NoError(t, tr.RecordCheckReport(ctx, tracking.Record{
		CandidateID: cand, HeadSHA: head, Phase: domain.PhaseCompleted,
		Conclusion: "success", DecisionID: decision.DecisionID, LastDecision: decision,
	}))
	raw, err := json.Marshal(decision)
	require.NoError(t, err)
	rec, _ := tr.LookupCheckReport(ctx, cand, head)
	back, err := json.Marshal(rec.LastDecision)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(back))
}
