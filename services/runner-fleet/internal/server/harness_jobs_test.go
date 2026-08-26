package server

// Job enqueue/claim helpers for the authenticated protocol harness.
// Split from harness_test.go to respect the 250-line cap (ENGINEERING_CHARTER §1).

import (
	"context"
	"net/http"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/joblease"
)

type testJobParams struct {
	runID      string
	attempt    int
	tier       int
	durationMS int64
	bias       string
	timeoutMS  int64
}

func (h *harness) mintLease(runID string, attempt int, fence int64, ttl time.Duration) string {
	h.t.Helper()
	now := h.clock()
	token, err := h.signer.Mint(joblease.Claims{
		Audience:   joblease.Audience,
		ID:         joblease.JTIBuilds(runID, attempt, fence),
		RunID:      runID,
		Attempt:    attempt,
		FenceToken: fence,
		Repo:       "acme/payments",
		Tier:       1,
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(ttl).Unix(),
	})
	if err != nil {
		h.t.Fatalf("mint lease for %s: %v", runID, err)
	}
	return token
}

// enqueue mints a dispatch-time credential and enqueues the job carrying it,
// exactly like control-plane dispatch (B2).
func (h *harness) enqueue(p testJobParams) domain.Job {
	h.t.Helper()
	if p.runID == "" {
		p.runID = "run_01JTEST0000000000000000000"
	}
	if p.attempt == 0 {
		p.attempt = 1
	}
	token := h.mintLease(p.runID, p.attempt, 1, 30*time.Minute)
	h.tokens[p.runID] = token
	return h.enqueueRawWithToken(p, token)
}

// enqueueToken attaches a fresh credential to an already-enqueued probe job
// and returns it for auth-matrix tests that need raw token strings.
func (h *harness) enqueueToken(p testJobParams) string {
	h.t.Helper()
	if p.runID == "" {
		p.runID = "run_01JTEST0000000000000000000"
	}
	if p.attempt == 0 {
		p.attempt = 1
	}
	token := h.mintLease(p.runID, p.attempt, 1, 30*time.Minute)
	h.tokens[p.runID] = token
	return token
}

func (h *harness) enqueueRaw(p testJobParams) domain.Job {
	return h.enqueueRawWithToken(p, "")
}

func (h *harness) enqueueRawWithToken(p testJobParams, leaseToken string) domain.Job {
	h.t.Helper()
	if p.runID == "" {
		p.runID = "run_01JTEST0000000000000000000"
	}
	if p.attempt == 0 {
		p.attempt = 1
	}
	if p.durationMS == 0 {
		p.durationMS = 50
	}
	if p.bias == "" {
		p.bias = "pass"
	}
	if p.timeoutMS == 0 {
		p.timeoutMS = 60000
	}
	job := domain.Job{
		RunID:      p.runID,
		Attempt:    p.attempt,
		Tier:       p.tier,
		Pool:       "sim",
		LeaseToken: leaseToken,
		Spec: domain.JobSpec{
			Kind:      "selected_unit",
			Repo:      "acme/payments",
			BaseSHA:   "1111111111111111111111111111111111111111",
			HeadSHA:   "2222222222222222222222222222222222222222",
			TimeoutMS: p.timeoutMS,
			SimProfile: &domain.SimProfile{
				DurationMS:  p.durationMS,
				OutcomeBias: p.bias,
			},
		},
	}
	if err := h.st.Enqueue(context.Background(), job); err != nil {
		h.t.Fatalf("enqueue %s: %v", p.runID, err)
	}
	return job
}

type claimedJobView struct {
	RunID      string
	FenceToken int64
	Attempt    int
	Tier       int
	Pool       string
	LeaseToken string
	Spec       domain.JobSpec
}

func (h *harness) claim(workerID string, limit int) []claimedJobView {
	return h.claimWithToken(workerID, limit)
}

// claimWithToken claims via HTTP; the response view carries the lease_token
// credential the fleet hands to the claiming worker.
func (h *harness) claimWithToken(workerID string, limit int) []claimedJobView {
	h.t.Helper()
	resp, body := h.post("/internal/fleet/jobs/claim", map[string]any{"pool": "sim", "limit": limit, "worker_id": workerID})
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("claim must 200, got %d (%v)", resp.StatusCode, body)
	}
	jobsAny, _ := body["jobs"].([]any)
	var out []claimedJobView
	for _, j := range jobsAny {
		m := j.(map[string]any)
		view := claimedJobView{RunID: m["run_id"].(string), FenceToken: int64(m["fence_token"].(float64))}
		if v, ok := m["attempt"].(float64); ok {
			view.Attempt = int(v)
		}
		if v, ok := m["tier"].(float64); ok {
			view.Tier = int(v)
		}
		if v, ok := m["pool"].(string); ok {
			view.Pool = v
		}
		if v, ok := m["lease_token"].(string); ok {
			view.LeaseToken = v
		}
		out = append(out, view)
	}
	return out
}

// tokenFor returns the credential the harness issued for a run.
func (h *harness) tokenFor(runID string) string { return h.tokens[runID] }
