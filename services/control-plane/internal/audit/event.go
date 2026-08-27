// Package audit defines the dedicated security-audit event stream
// (THREAT_MODEL B7): typed events, a bounded fire-and-forget worker, and the
// exact emission kinds. Persistence helpers live on the store; this package
// stays free of I/O so every consumer can depend on it without cycles.
package audit

import (
	"fmt"

	"github.com/oklog/ulid/v2"
)

// Kind enumerates the frozen security-audit event kinds (B7). Every emission
// point in the control-plane maps to exactly one kind; new points MUST add a
// constant here rather than passing raw strings.
type Kind string

// The seven binding B7 emission kinds.
const (
	// KindWebhookSignatureFailed fires when ingest reports a quarantined
	// signature-invalid webhook delivery (marker envelope seam).
	KindWebhookSignatureFailed Kind = "webhook_signature_failed"
	// KindAuthzRejected fires on 401/403 auth middleware rejections.
	KindAuthzRejected Kind = "authz_rejected"
	// KindAuthnAccepted fires on successful credential transitions
	// (email+password signup/login, SPEC §3 2026-08-26). Rejections of
	// those flows stay KindAuthzRejected (invalid_credentials reason).
	KindAuthnAccepted Kind = "authn_accepted"
	// KindFenceMismatch fires when a stale fence-token completion is
	// rejected at the ctrl completion gate (I-11).
	KindFenceMismatch Kind = "fence_mismatch"
	// KindBudgetExceeded fires on budget_exceeded admissions (I-10).
	KindBudgetExceeded Kind = "budget_exceeded"
	// KindEvidenceTamper fires when the evidence validator rejects
	// provenance or quarantines a digest-manifest mismatch (T2/T3).
	KindEvidenceTamper Kind = "evidence_tamper"
	// KindLeaseRevocation fires for teardown-class lease revocations
	// (reason tenant_teardown / repo_deleted), not routine supersedes.
	KindLeaseRevocation Kind = "lease_revocation"
	// KindChainVerifyFailure fires when the ledger chain verifier fails
	// (I-07 nightly verify).
	KindChainVerifyFailure Kind = "chain_verify_failure"
)

// Actor identifies WHO triggered the event (authenticated identity or the
// system component).
type Actor struct {
	Kind string // e.g. "anonymous" | "agent" | "system" | "github"
	ID   string
}

// Event is one security-audit row. Subject/Detail are pre-marshaled JSON
// objects: marshaling happens once at construction so emitters cannot mutate
// a queued event after Emit (the worker reads them concurrently).
type Event struct {
	ID       string
	TenantID string
	Kind     Kind
	Actor    Actor
	Subject  []byte // jsonb object bytes
	Detail   []byte // jsonb object bytes
}

// New builds an audit event with a fresh ULID primary key and validates that
// subject/detail marshal to JSON objects (never scalars) — jsonb columns
// would silently coerce otherwise.
func New(tenantID string, kind Kind, actor Actor, subject, detail map[string]any) (Event, error) {
	subjBytes, err := marshalObject(subject)
	if err != nil {
		return Event{}, fmt.Errorf("audit %s subject: %w", kind, err)
	}
	detBytes, err := marshalObject(detail)
	if err != nil {
		return Event{}, fmt.Errorf("audit %s detail: %w", kind, err)
	}
	return Event{
		ID:       ulid.Make().String(),
		TenantID: tenantID,
		Kind:     kind,
		Actor:    actor,
		Subject:  subjBytes,
		Detail:   detBytes,
	}, nil
}
