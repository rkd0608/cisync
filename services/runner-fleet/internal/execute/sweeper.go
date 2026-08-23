package execute

import (
	"context"
	"log/slog"
	"time"
)

// SweepStale requeues running jobs whose worker stopped heartbeating and
// purges dead workers; it runs once per tick until ctx ends.
func (e *Executor) SweepStale(ctx context.Context, threshold time.Duration, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			requeued, err := e.store.RequeueStale(ctx, threshold, e.nowFn())
			if err != nil {
				e.logger.Error("stale sweep failed", slog.String("err", err.Error()))
				continue
			}
			for _, runID := range requeued {
				e.logger.Warn("requeued stale job", slog.String("run_id", runID))
			}
		}
	}
}
