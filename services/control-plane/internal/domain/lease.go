package domain

import (
	"fmt"
	"time"
)

// LeaseState is the lifecycle state of a unified change-scope/environment lease.
type LeaseState string

// Lease states (DOMAIN_MODEL_DRAFT §1.8).
const (
	LeaseRequested LeaseState = "requested"
	LeaseGranted   LeaseState = "granted"
	LeaseReleased  LeaseState = "released"
	LeaseExpired   LeaseState = "expired"
	LeaseRevoked   LeaseState = "revoked"
)

var leaseTerminalStates = map[LeaseState]bool{
	LeaseReleased: true, LeaseExpired: true, LeaseRevoked: true,
}

// Terminal reports whether the state is terminal (I-08).
func (s LeaseState) Terminal() bool { return leaseTerminalStates[s] }

// LeaseScopeKind distinguishes change-scope from preview-environment leases.
type LeaseScopeKind string

// Lease scope kinds.
const (
	ScopeChangeScope LeaseScopeKind = "change_scope"
	ScopeEnvironment LeaseScopeKind = "environment"
)

// LeaseScope binds the surfaces (or env template) the lease covers.
type LeaseScope struct {
	Kind        LeaseScopeKind `json:"kind"`
	Surfaces    []string       `json:"surfaces,omitempty"`
	EnvTemplate string         `json:"env_template,omitempty"`
}

// Lease is a TTL-bounded scoped grant with reserved budget (invariant I-06).
type Lease struct {
	ID               string
	TenantID         string
	IntentID         string
	State            LeaseState
	Scope            LeaseScope
	Holder           string
	Budget           BudgetValues
	TTLExpiresAt     time.Time
	RenewalCount     int
	QueuePosition    *int
	EtaSeconds       *int
	RequiredEvidence []string
	CreatedAt        time.Time
	ReleasedAt       *time.Time
}

// NewLease constructs a granted lease with its first TTL window.
func NewLease(id, tenantID, intentID string, scope LeaseScope, holder string, budget BudgetValues, ttl time.Duration, requiredEvidence []string, now time.Time) *Lease {
	return &Lease{
		ID: id, TenantID: tenantID, IntentID: intentID, State: LeaseGranted,
		Scope: scope, Holder: holder, Budget: budget,
		TTLExpiresAt: now.Add(ttl), RequiredEvidence: requiredEvidence,
		CreatedAt: now,
	}
}

var leaseTransitions = map[string]transitionRule{
	"lease.granted":  {from: []string{string(LeaseRequested)}, to: string(LeaseGranted)},
	"lease.renewed":  {from: []string{string(LeaseGranted)}, to: string(LeaseGranted)},
	"lease.released": {from: []string{string(LeaseGranted)}, to: string(LeaseReleased)},
	"lease.expired":  {from: []string{string(LeaseGranted)}, to: string(LeaseExpired)},
	"lease.revoked":  {from: []string{string(LeaseGranted)}, to: string(LeaseRevoked)},
}

// Apply advances the lease's state machine on the named trigger. Terminal
// leases log-and-ignore every further event (I-08); expired/revoked leases
// cannot renew — a fresh grant is required.
func (l *Lease) Apply(trigger string) error {
	if l.State.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := leaseTransitions[trigger]
	if !ok {
		return fmt.Errorf("%w: %s unknown trigger for lease", ErrUnknownEvent, trigger)
	}
	if !matchesState(rule.from, string(l.State)) {
		return fmt.Errorf("%w: %s in %s via %s", ErrIllegalTransition, l.ID, l.State, trigger)
	}
	if trigger == "lease.released" || trigger == "lease.expired" || trigger == "lease.revoked" {
		now := time.Now().UTC()
		l.ReleasedAt = &now
	}
	l.State = LeaseState(rule.to)
	return nil
}

// Renew extends the TTL up to ttlSeconds and bumps renewal_count; it is a
// granted self-loop.
func (l *Lease) Renew(ttlSeconds int64) error {
	if err := l.Apply("lease.renewed"); err != nil {
		return err
	}
	if ttlSeconds < 30 || ttlSeconds > 3600 {
		return fmt.Errorf("%w: ttl_seconds must be within [30,3600]", ErrValidationFailed)
	}
	l.TTLExpiresAt = time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second)
	l.RenewalCount++
	return nil
}

// Expired reports whether the lease TTL has passed while still granted.
func (l *Lease) Expired(now time.Time) bool {
	return l.State == LeaseGranted && now.After(l.TTLExpiresAt)
}
