// Package domain holds pure delivery types and state transitions for ingest.
package domain

import "time"

// Delivery statuses.
const (
	StatusPending       = "pending"
	StatusForwarded     = "forwarded"
	StatusForwardFailed = "forward_failed"
	// StatusRejected quarantines signature-invalid deliveries: audit-only
	// rows that are never retried and never occupy the dedup slot (the
	// partial unique index covers sig_ok rows only).
	StatusRejected = "rejected"
	// StatusDuplicateSuspect marks fresh-GUID deliveries whose CONTENT
	// class matched something already seen inside the replay window (H2):
	// they are forwarded record-only, so the status is transient until the
	// forward outcome lands; the durable diagnosis rides
	// Delivery.DuplicateSuspect.
	StatusDuplicateSuspect = "duplicate_suspect"
)

// SourceGitHub is the only external source in v1.
const SourceGitHub = "github"

// Delivery is the audit anchor row persisted before any forwarding (T6).
type Delivery struct {
	ID            string
	Source        string
	ExtDeliveryID string
	EventKind     string
	Repo          string
	SigOK         bool
	Headers       map[string]string
	Payload       []byte
	Status        string
	Attempts      int
	ReceivedAt    time.Time
	LastAttemptAt time.Time
	ForwardedAt   time.Time
	// PayloadClassHash is sha256(source|repo|event_kind|normalized-significant-
	// payload-fields) — the H2 replay-window key (empty for legacy rows).
	PayloadClassHash string
	// DuplicateSuspect records a fresh-GUID near-replay hit inside the
	// seen-window; forwarded with a record-only diagnostic flag (H2).
	DuplicateSuspect bool
}

// Retryable reports whether a delivery is eligible for another forward attempt.
func (d *Delivery) Retryable(maxAttempts int) bool {
	return d.Status != StatusForwarded && d.Status != StatusRejected && d.Attempts < maxAttempts
}
