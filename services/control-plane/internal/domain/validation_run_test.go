package domain

import (
	"errors"
	"testing"
	"time"
)

func TestRunLifecycle(t *testing.T) {
	now := time.Now().UTC()
	spec := JobSpec{Kind: "hermetic_build", TimeoutMS: 900000}
	r := NewValidationRun("run_x", "org_test", "val_x", "cand_x", 1, spec, "sim", 900000, 20000, 0.9, now)

	if err := r.Apply("run.dispatched"); err != nil || r.State != RunDispatched {
		t.Fatalf("dispatch: %v %s", err, r.State)
	}
	if r.DispatchedAt == nil {
		t.Fatal("dispatch must stamp dispatched_at")
	}
	if err := r.Apply("run.claimed"); err != nil || r.State != RunRunning {
		t.Fatalf("claim: %v %s", err, r.State)
	}
	if err := r.Apply("run.failed"); err != nil || r.State != RunFailed {
		t.Fatalf("fail: %v %s", err, r.State)
	}
	beforeAttempt := r.Attempt
	if err := r.Apply("run.retry"); err != nil || r.State != RunQueued {
		t.Fatalf("retry: %v %s", err, r.State)
	}
	if r.Attempt != beforeAttempt+1 {
		t.Fatalf("retry must bump attempt: %d -> %d", beforeAttempt, r.Attempt)
	}
}

func TestRetryRequiresFreshFence(t *testing.T) {
	now := time.Now().UTC()
	spec := JobSpec{TimeoutMS: 1000}
	r := NewValidationRun("run_y", "org_test", "val_y", "cand_y", 0, spec, "sim", 1, 1, 1, now)
	_ = r.Apply("run.dispatched")
	fenceAfterDispatch := r.FenceToken
	if fenceAfterDispatch != 0 {
		t.Fatalf("domain leaves fence stamping to the store, got %d", fenceAfterDispatch)
	}
	_ = r.Apply("run.claimed")
	_ = r.Apply("run.failed")
	_ = r.Apply("run.retry")
	if r.FenceToken <= fenceAfterDispatch {
		t.Fatalf("retry must advance fence token: %d not > %d", r.FenceToken, fenceAfterDispatch)
	}
}

func TestCancelIgnoredAfterTerminal(t *testing.T) {
	now := time.Now().UTC()
	spec := JobSpec{TimeoutMS: 1000}
	r := NewValidationRun("run_z", "org_test", "val_z", "cand_z", 0, spec, "sim", 1, 1, 1, now)
	_ = r.Apply("run.dispatched")
	_ = r.Apply("run.claimed")
	_ = r.Apply("run.succeeded")
	err := r.Apply("run.cancelled")
	if !errors.Is(err, ErrPostTerminal) {
		t.Fatalf("cancel-after-complete must be ErrPostTerminal (I-08), got %v", err)
	}
	if r.State != RunSucceeded {
		t.Fatalf("state resurrected to %s", r.State)
	}
}

func TestEvidenceStateMachine(t *testing.T) {
	now := time.Now().UTC()
	e := NewEvidenceRecord("ev_x", "org_test", "run_x", 1, "cand_x", "hermetic_build", "pass",
		nil, InputsHash("b", "h", "p"), 0.9, 10, "lease_x", now)
	if err := e.Invalidate("base_advanced"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("invalidate proposed must be illegal, got %v", err)
	}
	if err := e.Apply("evidence.accepted"); err != nil || e.Status != EvidenceAccepted || e.AcceptedAt == nil {
		t.Fatalf("accept: %v %s", err, e.Status)
	}
	if err := e.Apply("evidence.rejected"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("re-accept/reject accepted must be illegal, got %v", err)
	}
	if err := e.Invalidate("ttl_expired"); err != nil || e.Status != EvidenceInvalidated || e.InvalidatedReason != "ttl_expired" {
		t.Fatalf("invalidate: %v %s %s", err, e.Status, e.InvalidatedReason)
	}
	if err := e.Apply("evidence.accepted"); !errors.Is(err, ErrPostTerminal) {
		t.Fatalf("post-terminal evidence must be ignored, got %v", err)
	}
}

func TestLeaseRenewExpire(t *testing.T) {
	now := time.Now().UTC()
	l := NewLease("lease_x", "org_test", "int_x", LeaseScope{Kind: ScopeChangeScope}, "agent:x",
		BudgetValues{CPUMinutes: 120}, time.Minute, []string{"hermetic_build"}, now)
	if l.Expired(now.Add(30 * time.Second)) {
		t.Fatal("fresh lease must not be expired")
	}
	if err := l.Renew(600); err != nil || l.RenewalCount != 1 {
		t.Fatalf("renew: %v count=%d", err, l.RenewalCount)
	}
	if err := l.Renew(10); !errors.Is(err, ErrValidationFailed) {
		t.Fatal("ttl below floor must fail validation")
	}
	if err := l.Apply("lease.expired"); err != nil || !l.Expired(now.Add(2*time.Hour)) && l.State != LeaseExpired {
		t.Fatalf("expire: %v state=%s", err, l.State)
	}
	if err := l.Renew(600); !errors.Is(err, ErrPostTerminal) {
		t.Fatal("expired lease must not renew")
	}
}
