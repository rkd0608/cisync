// Package rerun implements the check_run.rerequested policy layer (plan
// §4.5 / frozen ruling §10.2): policy knob, per-candidate and per-hour cost
// guardrails, and the control-plane revalidate client.
package rerun

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Policy selects the re-run behavior.
type Policy string

// Re-run policies (frozen ruling: default replan).
const (
	PolicyReplan       Policy = "replan"
	PolicyReplayCached Policy = "replay_cached"
)

// ParsePolicy validates CISYNC_CONN_RERUN_POLICY; empty ⇒ default replan.
func ParsePolicy(raw string) (Policy, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return PolicyReplan, nil
	case string(PolicyReplan):
		return PolicyReplan, nil
	case string(PolicyReplayCached):
		return PolicyReplayCached, nil
	default:
		return "", fmt.Errorf("rerun: invalid CISYNC_CONN_RERUN_POLICY %q (want replan|replay_cached)", raw)
	}
}

// Budget enforces the re-run cost guardrails:
//   - ≤ maxPerCandidate re-runs per candidate ever (process lifetime);
//   - ≤ ratePerHour re-runs per installation per fixed UTC hour window.
//
// WHY fixed-window hourly buckets instead of a sliding log: the cap is a
// blunt cost brake (§10.2), not an SLA — O(1) memory and deterministic tests
// beat boundary precision here.
type Budget struct {
	mu              sync.Mutex
	maxPerCandidate int
	ratePerHour     int
	perCandidate    map[string]int
	hourly          map[string]int // key: installationID + "|" + hour bucket
	now             func() time.Time
}

// NewBudget builds the guardrails.
func NewBudget(maxPerCandidate, ratePerHour int, now func() time.Time) *Budget {
	if now == nil {
		now = time.Now
	}
	return &Budget{
		maxPerCandidate: maxPerCandidate,
		ratePerHour:     ratePerHour,
		perCandidate:    make(map[string]int),
		hourly:          make(map[string]int),
		now:             now,
	}
}

// Verdict reports whether a re-run may proceed.
type Verdict struct {
	Allowed bool
	Reason  string // "candidate_cap" | "hour_rate" when blocked
}

func (b *Budget) Allow(candidateID string, installationID int64) Verdict {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.perCandidate[candidateID] >= b.maxPerCandidate {
		return Verdict{Allowed: false, Reason: "candidate_cap"}
	}
	bucket := installationID
	if installationID == 0 {
		bucket = -1 // dry-run/unresolved writes still share one conservative bucket
	}
	key := fmt.Sprintf("%d|%d", bucket, b.now().UTC().Unix()/3600)
	if b.hourly[key] >= b.ratePerHour {
		return Verdict{Allowed: false, Reason: "hour_rate"}
	}
	return Verdict{Allowed: true}
}

// Record consumes one re-run slot from both budgets after a successful
// dispatch decision.
func (b *Budget) Record(candidateID string, installationID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.perCandidate[candidateID]++
	bucket := installationID
	if installationID == 0 {
		bucket = -1
	}
	key := fmt.Sprintf("%d|%d", bucket, b.now().UTC().Unix()/3600)
	b.hourly[key]++
}

// Dedupe is a bounded TTL set of seen rerun idempotency keys so relay
// redeliveries don't double-burn budget between process restarts.
type Dedupe struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
	now  func() time.Time
}

// NewDedupe builds the TTL set (entries expire after ttl).
func NewDedupe(ttl time.Duration, now func() time.Time) *Dedupe {
	if now == nil {
		now = time.Now
	}
	return &Dedupe{seen: make(map[string]time.Time), ttl: ttl, now: now}
}

// FirstSeen records key and reports whether it was new.
func (d *Dedupe) FirstSeen(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	cutoff := d.now().Add(-d.ttl)
	for k, at := range d.seen {
		if at.Before(cutoff) {
			delete(d.seen, k)
		}
	}
	if _, dup := d.seen[key]; dup {
		return false
	}
	d.seen[key] = d.now()
	return true
}
