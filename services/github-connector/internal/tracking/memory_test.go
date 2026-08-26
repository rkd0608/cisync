package tracking

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/domain"
)

func TestUpsertMergesFieldsWithoutBlanking(t *testing.T) {
	store := NewMemoryStore(nil)
	ctx := context.Background()

	require.NoError(t, store.RecordCheckReport(ctx, Record{
		CandidateID: "cand_01J", HeadSHA: "aaaa", Repo: "acme/payments",
		CheckRunID: 42, Phase: domain.PhaseQueued,
	}))
	rec, err := store.LookupCheckReport(ctx, "cand_01J", "aaaa")
	require.NoError(t, err)
	require.Equal(t, int64(42), rec.CheckRunID)
	require.Equal(t, domain.PhaseQueued, rec.Phase)

	// Phase flip must not blank the tracked check_run_id.
	require.NoError(t, store.RecordCheckReport(ctx, Record{
		CandidateID: "cand_01J", HeadSHA: "aaaa", Phase: domain.PhaseCompleted, Conclusion: "success",
	}))
	rec, err = store.LookupCheckReport(ctx, "cand_01J", "aaaa")
	require.NoError(t, err)
	require.Equal(t, int64(42), rec.CheckRunID, "partial update preserves id")
	require.Equal(t, domain.PhaseCompleted, rec.Phase)

	_, err = store.LookupCheckReport(ctx, "cand_unknown", "aaaa")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestFindByDecisionIndexesLatestOnly(t *testing.T) {
	store := NewMemoryStore(nil)
	ctx := context.Background()
	env := &domain.DecisionEnvelope{DecisionID: "dec_01J", CandidateID: "cand_01J"}

	require.NoError(t, store.RecordCheckReport(ctx, Record{
		CandidateID: "cand_01J", HeadSHA: "aaaa", DecisionID: "dec_old",
	}))
	require.NoError(t, store.RecordCheckReport(ctx, Record{
		CandidateID: "cand_01J", HeadSHA: "aaaa", DecisionID: "dec_01J", LastDecision: env,
	}))

	rec, err := store.FindByDecision(ctx, "dec_01J")
	require.NoError(t, err)
	require.Same(t, env, rec.LastDecision)

	_, err = store.FindByDecision(ctx, "dec_missing")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestOpenCheckReportsFiltersTerminalAndOrdersOldestFirst(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	store := NewMemoryStore(func() time.Time { return clock })
	ctx := context.Background()

	mk := func(id string, phase domain.CheckPhase) Record {
		return Record{CandidateID: id, HeadSHA: "aaaa", Phase: phase}
	}
	require.NoError(t, store.RecordCheckReport(ctx, mk("cand_A", domain.PhaseQueued)))
	clock = now.Add(time.Minute)
	require.NoError(t, store.RecordCheckReport(ctx, mk("cand_B", domain.PhaseInProgress)))
	clock = now.Add(2 * time.Minute)
	require.NoError(t, store.RecordCheckReport(ctx, mk("cand_C", domain.PhaseCompleted)))

	open, err := store.OpenCheckReports(ctx, now.Add(10*time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, open, 2, "completed revisions excluded")
	require.Equal(t, "cand_A", open[0].CandidateID, "oldest update first")
	require.Equal(t, "cand_B", open[1].CandidateID)

	open, err = store.OpenCheckReports(ctx, now.Add(-time.Second), 10)
	require.NoError(t, err)
	require.Empty(t, open, "fresh revisions excluded by the staleness threshold")

	open, err = store.OpenCheckReports(ctx, now.Add(30*time.Second), 10)
	require.NoError(t, err)
	require.Len(t, open, 1)
	require.Equal(t, "cand_A", open[0].CandidateID)
}
