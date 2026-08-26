package verify

import (
	"context"
	"log/slog"
	"time"

	"sauron.dev/sauron/control-plane/internal/audit"
	"sauron.dev/sauron/control-plane/internal/store"
)

// VerifyFn abstracts one chain-verification pass so scheduling policy is
// testable without a database.
type VerifyFn func(ctx context.Context) (*Report, error)

// FromVerifier adapts the real Verifier to VerifyFn.
func FromVerifier(v *Verifier) VerifyFn {
	return v.Verify
}

// AuditSink persists chain-verification failures into the security-audit
// stream (B7 kind chain_verify_failure). Implemented by *store.Store.
type AuditSink interface {
	InsertSecurityAudit(ctx context.Context, ev audit.Event) error
}

// Scheduler runs the SAME verifier the `verify` subcommand runs, on an
// interval, inside the serving process (H3).
//
// WHY failure does NOT halt serving: automatic fail-closed here would turn a
// detected ledger anomaly into a platform outage without human judgment
// (availability tradeoff). The operator-facing posture stays manual/ops-side:
// the `verify` subcommand exits non-zero for gate scripts, while this loop
// makes every automated pass OBSERVABLE (structured log + metric) and every
// failure AUDITABLE (security_audit row) so operators decide about halting.
type Scheduler struct {
	verify   VerifyFn
	interval time.Duration
	audit    AuditSink // required; failures must land in ctrl.security_audit
	tenantID string
	// NotifyResult reports "ok" | "fail" per finished pass so main can bump
	// sauron_ledger_verify_result{status} on the shared registry.
	NotifyResult func(status string)
	// OnAuditEmitted fires after a chain-verify failure row persisted, for
	// sauron_security_audit_total{kind} parity.
	OnAuditEmitted func(kind string)
}

// NewScheduler wires the loop. interval <= 0 disables it (callers simply do
// not start the goroutine; Run guards defensively anyway).
func NewScheduler(fn VerifyFn, interval time.Duration, sink AuditSink, tenantID string, notify func(status string), onAudit func(kind string)) *Scheduler {
	return &Scheduler{
		verify:         fn,
		interval:       interval,
		audit:          sink,
		tenantID:       tenantID,
		NotifyResult:   notify,
		OnAuditEmitted: onAudit,
	}
}

// Run ticks until ctx is cancelled, verifying once immediately so a fresh
// process validates the chain before waiting a full interval.
func (s *Scheduler) Run(ctx context.Context) {
	if s.interval <= 0 {
		return
	}
	s.RunOnce(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunOnce(ctx)
		}
	}
}

// RunOnce executes ONE verification pass: structured log + metric always,
// security_audit row on failure.
func (s *Scheduler) RunOnce(ctx context.Context) {
	rep, err := s.verify(ctx)
	if err == nil {
		if s.NotifyResult != nil {
			s.NotifyResult("ok")
		}
		slog.Info("ledger chain verified",
			slog.Int("entries", rep.Entries), slog.Int("checkpoints", rep.Checkpoints))
		return
	}
	entries, checkpoints := 0, 0
	if rep != nil {
		entries, checkpoints = rep.Entries, rep.Checkpoints
	}
	if s.NotifyResult != nil {
		s.NotifyResult("fail")
	}
	slog.Error("ledger chain verification FAILED",
		slog.String("err", err.Error()),
		slog.Int("entries", entries), slog.Int("checkpoints", checkpoints))

	ev, evErr := audit.New(s.tenantID, audit.KindChainVerifyFailure,
		audit.Actor{Kind: "system", ID: "chain_verifier"},
		map[string]any{"component": "ctrl.ledger"},
		map[string]any{
			"error":       err.Error(),
			"entries":     entries,
			"checkpoints": checkpoints,
		})
	if evErr != nil {
		slog.Error("chain-verify audit event build failed", slog.String("err", evErr.Error()))
		return
	}
	if err := s.audit.InsertSecurityAudit(ctx, ev); err != nil {
		slog.Error("chain-verify audit row persist failed", slog.String("err", err.Error()))
		return
	}
	if s.OnAuditEmitted != nil {
		// Metric parity with streamed emissions elsewhere: this row was
		// persisted directly, so count it here.
		s.OnAuditEmitted(string(audit.KindChainVerifyFailure))
	}
}

// Compile-time guard: *store.Store satisfies AuditSink via its pool-level
// insert (used when no transaction is open).
var _ AuditSink = (*store.Store)(nil)
