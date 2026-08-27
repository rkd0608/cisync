// Package domain holds pure execution-plane types and state transitions.
package domain

// Job lifecycle statuses.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusTimedOut  = "timed_out"
	StatusCancelled = "cancelled"
)

// Terminal reports whether a status ends a job's lifecycle.
func Terminal(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusTimedOut, StatusCancelled:
		return true
	default:
		return false
	}
}

// SimProfile drives the deterministic simulation provider.
type SimProfile struct {
	DurationMS  int64  `json:"duration_ms"`
	OutcomeBias string `json:"outcome_bias"`
}

// JobSpec is the unit of work inside a claim payload (internal-protocols §3).
type JobSpec struct {
	Kind       string      `json:"kind"`
	Repo       string      `json:"repo"`
	BaseSHA    string      `json:"base_sha"`
	HeadSHA    string      `json:"head_sha"`
	PatchRef   string      `json:"patch_ref"`
	InputsHash string      `json:"inputs_hash"`
	TimeoutMS  int64       `json:"timeout_ms"`
	SimProfile *SimProfile `json:"sim_profile,omitempty"`
	// PreFetchedBundleRef carries the control-plane-materialized repo
	// snapshot (absolute path under the shared cisync-repos volume, keyed by
	// inputs_hash). Runners NEVER fetch with tokens themselves (B5): when
	// empty, real-exec providers degrade to honest all-skipped outcomes.
	PreFetchedBundleRef string `json:"pre_fetched_bundle_ref,omitempty"`
}

// Job is one fenced execution instance; run_id is unique per attempt.
// LeaseToken is the control-plane-minted job-lease JWT (THREAT_MODEL B2):
// presented as `Authorization: Bearer` on heartbeat/complete/cancel and
// re-verified by the embedded executor before any internal mutation (I-04).
type Job struct {
	RunID      string  `json:"run_id"`
	Attempt    int     `json:"attempt"`
	FenceToken int64   `json:"fence_token"`
	Tier       int     `json:"tier"`
	Pool       string  `json:"pool"`
	Spec       JobSpec `json:"job_spec"`
	LeaseToken string  `json:"lease_token,omitempty"`
}

// Artifact is a digest-addressed output blob.
type Artifact struct {
	Name        string `json:"name"`
	Digest      string `json:"digest"`
	SizeBytes   int64  `json:"size_bytes"`
	Content     []byte `json:"-"`
	ContentType string `json:"content_type,omitempty"`
}

// TestResults is the runner-reported outcome census (internal-protocols §2
// results object). It rides on every terminal Outcome so control-plane can
// validate I-01 against REAL executed tests instead of job status.
type TestResults struct {
	Total       int `json:"total"`
	Passed      int `json:"passed"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
	Quarantined int `json:"quarantined"`
}

// Sum returns the total of all counted outcomes; consistency requires it to
// equal Total exactly.
func (r TestResults) Sum() int {
	return r.Passed + r.Failed + r.Skipped + r.Quarantined
}

// Outcome is a terminal execution result reported through complete.
type Outcome struct {
	Status         string // succeeded | failed | timed_out
	Classification string // e.g. "" | flake | deterministic_regression | infra_transient | exit_nonzero
	Logs           []byte
	Artifacts      []Artifact
	DurationMS     int64
	CostMilliCents int64
	// Results is the outcome census; providers MUST populate it (P0-2).
	Results *TestResults
}
