// Package forward sends normalized deliveries to control-plane per
// packages/contracts/internal-protocols.md §1.
package forward

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"cisync.dev/cisync/ingest/internal/redact"
)

// Envelope is the wire shape of POST /internal/ctrl/deliveries.
type Envelope struct {
	Source        string          `json:"source"`
	ExtDeliveryID string          `json:"ext_delivery_id"`
	EventKind     string          `json:"event_kind"`
	Repo          string          `json:"repo"`
	ReceivedAt    time.Time       `json:"received_at"`
	Payload       json.RawMessage `json:"payload"`
	// DuplicateSuspect (H2, additive): fresh-GUID content whose class hash
	// hit the replay seen-window; control-plane logs it record-only with
	// NO domain effects. Omitted for normal traffic.
	DuplicateSuspect bool `json:"duplicate_suspect,omitempty"`
	// QuarantineReason (B7, additive): non-empty only on signature-failure
	// audit markers minted by ingest; control-plane records a
	// security_audit row and applies no effects.
	QuarantineReason string `json:"quarantine_reason,omitempty"`
}

// Result classifies a forwarding attempt.
type Result int

// Forward outcomes.
const (
	ResultAccepted Result = iota
	ResultUnavailable
	ResultRejected
)

// Forwarder posts redacted, HMAC-signed envelopes to control-plane.
type Forwarder struct {
	BaseURL string
	Secret  []byte
	Client  *http.Client
}

// New returns a Forwarder with sane transport defaults.
func New(baseURL string, secret []byte) *Forwarder {
	return &Forwarder{
		BaseURL: baseURL,
		Secret:  secret,
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// RedactPayload scrubs secret-bearing keys and value shapes from raw payload
// bytes; on failure it returns the fail-closed tombstone produced by the
// scrubber so no unredacted byte can leave the process (T1).
func RedactPayload(raw []byte) ([]byte, error) {
	out, err := redact.Payload(raw)
	if err != nil {
		return out, fmt.Errorf("forward: fail-closed redaction applied: %w", err)
	}
	return out, nil
}

// Send performs one delivery attempt against control-plane.
func (f *Forwarder) Send(ctx context.Context, env Envelope) (Result, error) {
	body, err := json.Marshal(env)
	if err != nil {
		return ResultRejected, fmt.Errorf("forward: marshal envelope: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.BaseURL+"/internal/ctrl/deliveries", bytes.NewReader(body))
	if err != nil {
		return ResultRejected, fmt.Errorf("forward: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", env.ExtDeliveryID)
	mac := hmac.New(sha256.New, f.Secret)
	mac.Write(body)
	req.Header.Set("X-CISync-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))

	resp, err := f.Client.Do(req)
	if err != nil {
		return ResultUnavailable, fmt.Errorf("forward: post delivery %s: %w", env.ExtDeliveryID, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted:
		return ResultAccepted, nil
	case resp.StatusCode == http.StatusServiceUnavailable:
		return ResultUnavailable, fmt.Errorf("forward: control-plane unavailable for %s: status %d", env.ExtDeliveryID, resp.StatusCode)
	default:
		return ResultRejected, fmt.Errorf("forward: control-plane rejected %s: status %d", env.ExtDeliveryID, resp.StatusCode)
	}
}
