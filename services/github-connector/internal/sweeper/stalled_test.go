package sweeper

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/emit"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// recordingRouter captures emit calls without any GitHub dependency.
type recordingRouter struct {
	creates  []checks.CheckPayload
	updates  []checks.CheckPayload
	updateID int64
}

func (r *recordingRouter) Create(_ context.Context, _ string, payload checks.CheckPayload) (emit.Result, error) {
	r.creates = append(r.creates, payload)
	return emit.Result{DryRun: true}, nil
}

func (r *recordingRouter) Update(_ context.Context, _ string, checkRunID int64, payload checks.CheckPayload) (emit.Result, error) {
	r.updates = append(r.updates, payload)
	r.updateID = checkRunID
	return emit.Result{CheckRunID: checkRunID}, nil
}

func newHarness(t *testing.T, now func() time.Time) (*Sweeper, *tracking.MemoryStore, *recordingRouter) {
	t.Helper()
	store := tracking.NewMemoryStore(now)
	router := &recordingRouter{}
	sw := New(store, router, "http://web", 45*time.Minute, time.Minute, now,
		slog.New(slog.NewTextHandler(discard{}, nil)))
	return sw, store, router
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestStaleOpenChecksFlipToNeutral(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	sw, store, router := newHarness(t, func() time.Time { return clock })
	ctx := context.Background()

	require.NoError(t, store.RecordCheckReport(ctx, tracking.Record{
		CandidateID: "cand_old", HeadSHA: "aaaa", Repo: "acme/payments",
		CheckRunID: 77, Phase: domain.PhaseInProgress,
	}))
	// Fresh revision registered moments before the sweep.
	clock = now.Add(49 * time.Minute)
	require.NoError(t, store.RecordCheckReport(ctx, tracking.Record{
		CandidateID: "cand_fresh", HeadSHA: "bbbb", Repo: "acme/payments",
		CheckRunID: 88, Phase: domain.PhaseQueued,
	}))

	clock = now.Add(50 * time.Minute)
	sw.tick(ctx)

	require.Len(t, router.updates, 1, "only the stale in-progress run flips")
	flipped := router.updates[0]
	require.Equal(t, "completed", flipped.Status)
	require.Equal(t, "neutral", flipped.Conclusion)
	require.Equal(t, int64(77), router.updateID)
	require.Contains(t, flipped.Summary, "**Stalled**")

	rec, err := store.LookupCheckReport(ctx, "cand_old", "aaaa")
	require.NoError(t, err)
	require.Equal(t, domain.PhaseCompleted, rec.Phase)
	require.Equal(t, "neutral", rec.Conclusion)
	require.True(t, rec.Stalled)
}

func TestFreshAndTerminalChecksUntouched(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	sw, store, router := newHarness(t, func() time.Time { return clock })
	ctx := context.Background()

	clock = now.Add(45 * time.Minute)
	require.NoError(t, store.RecordCheckReport(ctx, tracking.Record{
		CandidateID: "cand_fresh", HeadSHA: "aaaa", Repo: "acme/payments",
		CheckRunID: 1, Phase: domain.PhaseQueued,
	}))
	require.NoError(t, store.RecordCheckReport(ctx, tracking.Record{
		CandidateID: "cand_done", HeadSHA: "bbbb", Repo: "acme/payments",
		CheckRunID: 2, Phase: domain.PhaseCompleted, Conclusion: "success",
	}))
	// Fresh record is 1 minute old at sweep time — well under 45m.
	clock = now.Add(46 * time.Minute)
	sw.tick(ctx)
	require.Empty(t, router.updates, "fresh + terminal revisions never flip")
}

func TestUntrackedRunCreatesNeutralOnSweep(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	sw, store, router := newHarness(t, func() time.Time { return clock })
	ctx := context.Background()

	// Dry-run lineage: phase tracked but no GitHub id yet.
	require.NoError(t, store.RecordCheckReport(ctx, tracking.Record{
		CandidateID: "cand_norun", HeadSHA: "cccc", Repo: "acme/payments",
		Phase: domain.PhaseQueued,
	}))
	clock = now.Add(50 * time.Minute)
	sw.tick(ctx)

	require.Len(t, router.creates, 1, "no id ⇒ create instead of update")
	require.Equal(t, "neutral", router.creates[0].Conclusion)
}
