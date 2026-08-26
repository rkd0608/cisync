package rerun

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnknownCandidate reports the revalidate endpoint answering 404.
type ErrUnknownCandidate struct{ CandidateID string }

func (e *ErrUnknownCandidate) Error() string {
	return "rerun: control-plane has no candidate " + e.CandidateID
}

// ErrBudgetExhausted reports ctrl 409 conflict_state
// (details.reason=rerun_budget_exhausted): the candidate's revalidation cap
// is spent or the candidate reached a terminal state — a permanent verdict,
// NOT a transient unavailability.
type ErrBudgetExhausted struct{ CandidateID string }

func (e *ErrBudgetExhausted) Error() string {
	return "rerun: control-plane declined revalidation of " + e.CandidateID + " (budget exhausted or terminal)"
}

// Control is the thin client for the control-plane revalidate command
// (internal-protocols §4): POST /v1/candidates/{id}/revalidate, admin-bearer
// auth. A nil/empty baseURL disables the replan policy at the call site —
// the feature flag posture for deployments without a reachable ctrl.
type Control struct {
	baseURL string
	token   string
	http    *http.Client
	now     func() time.Time
}

// NewControl builds the client; baseURL "" means unreachable-by-config.
func NewControl(baseURL, adminToken string, httpClient *http.Client, now func() time.Time) *Control {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Control{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   adminToken,
		http:    httpClient,
		now:     now,
	}
}

// Enabled reports whether replan can even be attempted.
func (c *Control) Enabled() bool { return c.baseURL != "" }

// Revalidate asks control-plane to append a re-plan command for the
// candidate under CURRENT policy + current inputs_hash (plan §4.5).
// idempotencyKey MUST be the originating GitHub ext_delivery_id so ctrl's
// command_log collapses redelivered re-runs (contract §4.4: required,
// 16..128 chars). 202 ⇒ accepted · 404 ⇒ ErrUnknownCandidate ·
// 409 ⇒ ErrBudgetExhausted · anything else ⇒ error.
func (c *Control) Revalidate(ctx context.Context, candidateID, idempotencyKey string) error {
	if !c.Enabled() {
		return fmt.Errorf("rerun: control-plane URL not configured")
	}
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 128 {
		return fmt.Errorf("rerun: revalidate Idempotency-Key must be 16..128 chars, got %d", len(idempotencyKey))
	}
	url := fmt.Sprintf("%s/v1/candidates/%s/revalidate", c.baseURL, candidateID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return fmt.Errorf("rerun: build revalidate request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("rerun: revalidate call failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch resp.StatusCode {
	case http.StatusAccepted:
		return nil
	case http.StatusNotFound:
		return &ErrUnknownCandidate{CandidateID: candidateID}
	case http.StatusConflict:
		return &ErrBudgetExhausted{CandidateID: candidateID}
	case http.StatusUnauthorized:
		return fmt.Errorf("rerun: revalidate rejected (auth); check CISYNC_CONN_CTRL_TOKEN")
	default:
		return fmt.Errorf("rerun: revalidate unexpected status %d", resp.StatusCode)
	}
}
