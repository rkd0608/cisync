// Package store defines the persistence contract for the fleet execution
// plane. Postgres is the state authority (D1); the interface lets test doubles
// and future engines slot in.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
)

// Completion is the complete-request payload gated by fence token (I-11).
type Completion struct {
	FenceToken           int64
	Status               string
	LogsDigest           string
	ArtifactDigests      []string
	DurationMS           int64
	ActualCostMilliCents int64
	Classification       string
	// LogsExcerpt carries a bounded prefix of the run log so control-plane
	// can classify failures without cross-schema reads. Sim-only today.
	LogsExcerpt string
	// Results is the runner-reported outcome census (P0-2/I-01); nil is
	// tolerated for synthetic probes but real jobs must populate it.
	Results *domain.TestResults
}

// ResultRef renders the stored result document for a completion, including
// the census and its tamper-evident digest when present.
func (c Completion) ResultRef() map[string]any {
	ref := map[string]any{
		"status":          c.Status,
		"logs_digest":     c.LogsDigest,
		"duration_ms":     c.DurationMS,
		"cost_millicents": c.ActualCostMilliCents,
	}
	if c.Classification != "" {
		ref["class"] = c.Classification
	}
	if len(c.ArtifactDigests) > 0 {
		ref["artifact_digests"] = append([]string(nil), c.ArtifactDigests...)
	}
	if c.LogsExcerpt != "" {
		ref["logs_excerpt"] = c.LogsExcerpt
	}
	if c.Results != nil {
		raw, err := json.Marshal(*c.Results)
		if err == nil {
			var doc map[string]any
			if json.Unmarshal(raw, &doc) == nil {
				ref["results"] = doc
				sum := sha256.Sum256(raw)
				ref["results_digest"] = "sha256:" + hex.EncodeToString(sum[:])
			}
		}
	}
	return ref
}

// FleetJob is a stored execution job including mutable claim state.
type FleetJob struct {
	ID            string
	RunID         string
	Attempt       int
	Tier          int
	Pool          string
	Status        string
	FenceToken    int64
	ClaimedBy     string
	ClaimedAt     time.Time
	LastHeartbeat time.Time
	FinishedAt    time.Time
	ResultRef     map[string]any
	Accepted      bool
	Spec          domain.JobSpec
	// LeaseToken is the dispatch-time credential (B2); empty only for
	// synthetic probe jobs, whose mutations are rejected as unauthorized.
	LeaseToken string
	CreatedAt  time.Time
}

// Claim identifies the claiming worker and its capability filter.
type Claim struct {
	Pool     string
	Provider string
	Limit    int
	WorkerID string
}

// Store persists execution jobs, worker registry, and digest-addressed
// artifacts. Implementations must make ClaimJobs atomic so at most one worker
// holds a run at a time.
type Store interface {
	// Enqueue inserts a job in status queued with fence_token 0.
	Enqueue(ctx context.Context, job domain.Job) error
	// ClaimJobs atomically claims up to c.Limit queued jobs of pool for the
	// worker, bumping each fence_token (epoch) and setting running state.
	ClaimJobs(ctx context.Context, c Claim, now time.Time) ([]domain.Job, error)
	// EnsureWorker registers worker liveness once per slot; claim transactions
	// must never touch this table (hot-row convoy — see pg_lifecycle.go).
	EnsureWorker(ctx context.Context, id string, pool string, capacity int, now time.Time) error
	// Get fetches one job by run_id.
	Get(ctx context.Context, runID string) (FleetJob, error)
	// Heartbeat validates fence+running state and refreshes last_heartbeat.
	Heartbeat(ctx context.Context, runID string, fenceToken int64, now time.Time) error
	// Complete applies the fenced completion gate: only the current epoch may
	// accept; acceptance is at-most-once per run/attempt (I-11).
	Complete(ctx context.Context, runID string, c Completion, now time.Time) error
	// Cancel marks a non-terminal job cancelled, bumping its epoch so stale
	// workers are rejected afterwards. Returns false when already terminal.
	Cancel(ctx context.Context, runID string, reason string, now time.Time) (bool, error)
	// RecordArtifacts upserts artifact metadata rows for a run.
	RecordArtifacts(ctx context.Context, runID string, artifacts []domain.Artifact, now time.Time) error
	// RequeueStale requeues running jobs whose heartbeat is older than
	// threshold, bumping epochs; returns the requeued run_ids.
	RequeueStale(ctx context.Context, threshold time.Duration, now time.Time) ([]string, error)
	// QueueDepth counts queued jobs per pool/tier.
	QueueDepth(ctx context.Context) (map[string]int64, error)
	// TerminalAccepted returns accepted terminal jobs (succeeded/failed/
	// timed_out/cancelled) most recent first — the completion feed
	// control-plane polls; consumers dedupe by (run_id, fence_token).
	TerminalAccepted(ctx context.Context, limit int) ([]FleetJob, error)
}
