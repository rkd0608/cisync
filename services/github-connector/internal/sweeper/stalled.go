// Package sweeper is the stalled-check safety net (plan §4.2): check runs
// stuck non-completed beyond SAURON_CONN_STALLED_CHECK_AGE flip to neutral
// so a required check can never stay yellow forever.
package sweeper

import (
	"context"
	"log/slog"
	"time"

	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/emit"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// Publisher is the emit surface the sweeper needs (interface so tests can
// record without a real router).
type Publisher interface {
	Create(ctx context.Context, repo string, payload checks.CheckPayload) (emit.Result, error)
	Update(ctx context.Context, repo string, checkRunID int64, payload checks.CheckPayload) (emit.Result, error)
}

// Sweeper periodically flips stalled open checks to neutral.
type Sweeper struct {
	store    tracking.Store
	router   Publisher
	details  string
	maxAge   time.Duration
	interval time.Duration
	now      func() time.Time
	logger   *slog.Logger
}

// New builds the sweeper. now and interval are injectable for tests.
func New(store tracking.Store, router Publisher, details string, maxAge, interval time.Duration, now func() time.Time, logger *slog.Logger) *Sweeper {
	if now == nil {
		now = time.Now
	}
	return &Sweeper{
		store: store, router: router, details: details,
		maxAge: maxAge, interval: interval, now: now, logger: logger,
	}
}

// Run blocks until ctx cancels; intended as its own goroutine.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Sweeper) tick(ctx context.Context) {
	open, err := s.store.OpenCheckReports(ctx, s.now().Add(-s.maxAge), 100)
	if err != nil {
		s.logger.Error("stalled sweep lookup failed", slog.String("err", err.Error()))
		return
	}
	for _, rec := range open {
		if err := s.flipOne(ctx, rec); err != nil {
			s.logger.Error("stalled flip failed",
				slog.String("candidate_id", rec.CandidateID), slog.String("err", err.Error()))
		}
	}
}

func (s *Sweeper) flipOne(ctx context.Context, rec tracking.Record) error {
	payload := checks.RenderStalled(rec.CandidateID, rec.HeadSHA, s.details, s.now())
	var result emit.Result
	var err error
	if rec.CheckRunID > 0 {
		result, err = s.router.Update(ctx, rec.Repo, rec.CheckRunID, payload)
	} else {
		result, err = s.router.Create(ctx, rec.Repo, payload)
	}
	if err != nil {
		return err
	}
	checkRunID := result.CheckRunID
	if result.Queued {
		// The neutral flip sits in the pending outbox; keep the row open
		// until the drain confirms. Next sweep retries the flip.
		return nil
	}
	err = s.store.RecordCheckReport(ctx, tracking.Record{
		CandidateID: rec.CandidateID,
		HeadSHA:     rec.HeadSHA,
		Repo:        rec.Repo,
		CheckRunID:  checkRunID,
		Phase:       domain.PhaseCompleted,
		Conclusion:  "neutral",
		Stalled:     true,
	})
	if err != nil {
		return err
	}
	s.logger.Warn("flipped stalled check to neutral",
		slog.String("candidate_id", rec.CandidateID),
		slog.String("repo", rec.Repo),
		slog.Int64("check_run_id", checkRunID))
	return nil
}
