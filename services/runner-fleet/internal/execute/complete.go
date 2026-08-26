package execute

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/providers"
	"cisync.dev/cisync/runner-fleet/internal/store"
)

// maxLogsExcerptBytes bounds the log prefix carried in result_ref so
// control-plane can classify failures without fetching artifacts.
const maxLogsExcerptBytes = 4096

// completeInternal is the executor-side completion path. The job-lease gate
// runs FIRST: a fence rejection or an unprovable lease means the job was
// cancelled, reclaimed elsewhere, or never legitimately dispatched — the
// result MUST be discarded without mutating state (I-11/I-04).
func (e *Executor) completeInternal(ctx context.Context, job domain.Job, outcome domain.Outcome) {
	if !e.authorizeInternal(job) {
		e.metrics.CounterInc("fleet_completions_rejected_total", "Completions rejected by the fence gate", "reason", "unauthorized")
		return
	}
	digests := make([]string, 0, len(outcome.Artifacts))
	for _, a := range outcome.Artifacts {
		digests = append(digests, a.Digest)
	}
	logsDigest := providers.DigestOf(outcome.Logs)
	excerpt := outcome.Logs
	if len(excerpt) > maxLogsExcerptBytes {
		excerpt = excerpt[:maxLogsExcerptBytes]
	}
	err := e.store.Complete(ctx, job.RunID, store.Completion{
		FenceToken:           job.FenceToken,
		Status:               outcome.Status,
		LogsDigest:           logsDigest,
		ArtifactDigests:      digests,
		DurationMS:           outcome.DurationMS,
		ActualCostMilliCents: outcome.CostMilliCents,
		Classification:       outcome.Classification,
		LogsExcerpt:          string(excerpt),
		Results:              outcome.Results,
	}, e.nowFn())
	switch {
	case err == nil:
		if recordErr := e.store.RecordArtifacts(ctx, job.RunID, outcome.Artifacts, e.nowFn()); recordErr != nil {
			e.logger.Warn("artifact recording failed", slog.String("run_id", job.RunID), slog.String("err", recordErr.Error()))
		}
		e.metrics.CounterInc("fleet_completions_total", "Accepted job completions", "status", outcome.Status)
		e.logger.Info("job completed",
			slog.String("run_id", job.RunID),
			slog.String("status", outcome.Status),
			slog.String("logs_digest", logsDigest))
	case errors.Is(err, domain.ErrFenceMismatch):
		e.metrics.CounterInc("fleet_completions_rejected_total", "Completions rejected by the fence gate", "reason", "fence_mismatch")
		e.logger.Warn("stale fence on internal completion; result discarded",
			slog.String("run_id", job.RunID), slog.String("err", err.Error()))
	default:
		e.metrics.CounterInc("fleet_completions_rejected_total", "Completions rejected by the fence gate", "reason", "already_accepted")
		e.logger.Warn("completion not accepted", slog.String("run_id", job.RunID), slog.String("err", err.Error()))
	}
}

// heartbeatOnce sends one internal heartbeat behind the same job-lease gate;
// a rejected credential stops the heartbeat loop for this job.
func (e *Executor) heartbeatOnce(job domain.Job) bool {
	if !e.authorizeInternal(job) {
		return false
	}
	hbCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := e.store.Heartbeat(hbCtx, job.RunID, job.FenceToken, e.nowFn())
	if err != nil {
		e.logger.Warn("heartbeat rejected", slog.String("run_id", job.RunID), slog.String("err", err.Error()))
	}
	return err == nil
}
