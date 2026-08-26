// Package providers implements the domain.Provider execution adapters.
package providers

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// SimHandle tracks one in-flight simulated execution.
type SimHandle struct {
	job       domain.Job
	startedAt time.Time
	duration  time.Duration
	outcome   domain.Outcome
	done      bool
	cancelled bool
}

// SimProvider is the deterministic CI-default provider (ARCHITECTURE §8): no
// code executes; duration and outcome derive from sha256(run_id:attempt) so
// identical inputs always reproduce identical results.
type SimProvider struct {
	mu      sync.Mutex
	handles map[*SimHandle]struct{}
	nowFn   func() time.Time
}

// NewSim returns an empty simulation provider.
func NewSim() *SimProvider {
	return &SimProvider{handles: make(map[*SimHandle]struct{}), nowFn: time.Now}
}

// Submit seeds the simulation from run_id and attempt and prepares the outcome
// without executing anything (safe by construction, B5).
func (p *SimProvider) Submit(_ context.Context, job domain.Job) (domain.Handle, error) {
	rng := rand.New(rand.NewSource(SeedInt(job.RunID, job.Attempt)))
	duration, outcome := simulate(job.Spec, rng)
	h := &SimHandle{
		job:       job,
		startedAt: p.nowFn(),
		duration:  duration,
		outcome:   outcome,
	}
	p.mu.Lock()
	p.handles[h] = struct{}{}
	p.mu.Unlock()
	return h, nil
}

// Cancel marks the handle cancelled; a later Poll reports the cancellation.
func (p *SimProvider) Cancel(handle domain.Handle) error {
	h, ok := handle.(*SimHandle)
	if !ok {
		return fmt.Errorf("sim provider: foreign handle %T", handle)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !h.done {
		h.cancelled = true
	}
	return nil
}

// Poll advances the simulated clock and reports Running or the terminal
// Outcome exactly once.
func (p *SimProvider) Poll(handle domain.Handle) (domain.PollState, domain.Outcome) {
	h, ok := handle.(*SimHandle)
	if !ok {
		return domain.PollDone, domain.Outcome{Status: domain.StatusFailed, Classification: "infra_transient"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if h.done {
		return domain.PollDone, h.outcome
	}
	if h.cancelled {
		h.done = true
		h.outcome.Status = domain.StatusCancelled
		return domain.PollDone, h.outcome
	}
	if p.nowFn().Sub(h.startedAt) < h.duration {
		return domain.PollRunning, domain.Outcome{}
	}
	h.done = true
	finalizeOutcome(&h.outcome)
	return domain.PollDone, h.outcome
}

// SeedInt derives a deterministic rand source seed from run id + attempt.
func SeedInt(runID string, attempt int) int64 {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", runID, attempt)))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func simulate(spec domain.JobSpec, rng *rand.Rand) (time.Duration, domain.Outcome) {
	baseMS := int64(1000)
	bias := "pass"
	if spec.SimProfile != nil {
		if spec.SimProfile.DurationMS > 0 {
			baseMS = spec.SimProfile.DurationMS
		}
		if spec.SimProfile.OutcomeBias != "" {
			bias = spec.SimProfile.OutcomeBias
		}
	}
	jittered := float64(baseMS) * (0.8 + 0.4*rng.Float64())
	duration := time.Duration(jittered * float64(time.Millisecond))

	status := domain.StatusSucceeded
	classification := ""
	switch {
	case bias == "pass":
	case strings.HasPrefix(bias, "fail:"):
		status = domain.StatusFailed
		classification = strings.TrimPrefix(bias, "fail:")
	case strings.HasPrefix(bias, "flaky:"):
		p, err := strconv.ParseFloat(strings.TrimPrefix(bias, "flaky:"), 64)
		if err != nil || rng.Float64() >= p {
			status = domain.StatusFailed
			classification = "flake"
		}
	default:
		status = domain.StatusFailed
		classification = "unknown_outcome_bias"
	}
	// Deterministic census consistent with the bias (P0-2): total derives
	// from the seeded rng so identical inputs reproduce identical results.
	total := 6 + rng.Intn(7)
	results := domain.TestResults{Total: total}
	if status == domain.StatusSucceeded {
		results.Passed = total
	} else {
		results.Failed = 1 + rng.Intn(total/3+1)
		results.Passed = total - results.Failed
	}
	return duration, domain.Outcome{
		Status:         status,
		Classification: classification,
		DurationMS:     duration.Milliseconds(),
		Results:        &results,
	}
}

func finalizeOutcome(o *domain.Outcome) {
	logs := SimLogs(o)
	o.Logs = logs
	o.Artifacts = []domain.Artifact{
		simArtifact("report.json", o),
	}
}

// SimLogs renders the deterministic log text for a simulated outcome. It is
// exported for reuse by tests that need byte-identical digests.
func SimLogs(o *domain.Outcome) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "[sim] outcome=%s classification=%s\n", o.Status, o.Classification)
	fmt.Fprintf(&b, "[sim] duration_ms=%d\n", o.DurationMS)
	return []byte(b.String())
}

// DigestOf computes the canonical sha256:<hex> digest for arbitrary bytes.
func DigestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func simArtifact(name string, o *domain.Outcome) domain.Artifact {
	body, _ := json.Marshal(map[string]any{
		"status":         o.Status,
		"classification": o.Classification,
		"duration_ms":    o.DurationMS,
	})
	return domain.Artifact{
		Name:        name,
		Digest:      DigestOf(body),
		SizeBytes:   int64(len(body)),
		Content:     body,
		ContentType: "application/json",
	}
}
