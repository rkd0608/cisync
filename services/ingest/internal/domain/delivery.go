// Package domain holds pure delivery types and state transitions for ingest.
package domain

import "time"

// Delivery statuses.
const (
	StatusPending       = "pending"
	StatusForwarded     = "forwarded"
	StatusForwardFailed = "forward_failed"
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
}

// Retryable reports whether a delivery is eligible for another forward attempt.
func (d *Delivery) Retryable(maxAttempts int) bool {
	return d.Status != StatusForwarded && d.Attempts < maxAttempts
}
