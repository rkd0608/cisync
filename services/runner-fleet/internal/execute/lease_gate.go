package execute

import (
	"log/slog"

	"sauron.dev/sauron/runner-fleet/internal/domain"
)

// authorizeInternal re-verifies the dispatch-time job-lease credential before
// the embedded executor mutates job state. THREAT_MODEL B2 requires the fleet
// to verify the lease BEFORE complete — the in-process path is held to the
// same standard as the HTTP handlers, so a worker that cannot prove its lease
// never writes results (I-04 claim binding: run_id + attempt + fence).
func (e *Executor) authorizeInternal(job domain.Job) bool {
	if e.LeaseVerifier == nil {
		e.logger.Error("job-lease verifier not configured; failing closed",
			slog.String("run_id", job.RunID))
		return false
	}
	if job.LeaseToken == "" {
		e.logger.Warn("claimed job carries no job-lease credential; result will be discarded",
			slog.String("run_id", job.RunID))
		return false
	}
	claims, err := e.LeaseVerifier.Verify(job.LeaseToken)
	if err != nil {
		e.logger.Warn("job-lease verification failed for claimed job",
			slog.String("run_id", job.RunID))
		return false
	}
	return claims.RunID == job.RunID &&
		claims.Attempt == job.Attempt &&
		claims.FenceToken == job.FenceToken
}
