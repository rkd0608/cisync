package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// FleetClient speaks internal-protocols §2 to runner-fleet.
type FleetClient struct {
	baseURL string
	http    *http.Client
}

// NewFleetClient constructs a client against SAURON_CTRL_FLEET_URL.
func NewFleetClient(baseURL string) *FleetClient {
	return &FleetClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// ClaimJob is one job echoed by the fleet claim endpoint.
type ClaimJob struct {
	RunID      string         `json:"run_id"`
	Attempt    int            `json:"attempt"`
	FenceToken int64          `json:"fence_token"`
	Tier       int            `json:"tier"`
	Pool       string         `json:"pool"`
	JobSpec    map[string]any `json:"job_spec"`
}

// Claim POSTs /internal/fleet/jobs/claim and returns the claimed jobs.
func (c *FleetClient) Claim(ctx context.Context, pool string, limit int) ([]ClaimJob, error) {
	body, _ := json.Marshal(map[string]any{"pool": pool, "limit": limit})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/fleet/jobs/claim", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("fleet claim request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fleet claim call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet claim status %d", resp.StatusCode)
	}
	var parsed struct {
		Jobs []ClaimJob `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("fleet claim decode: %w", err)
	}
	return parsed.Jobs, nil
}

// Cancel POSTs /internal/fleet/jobs/{run_id}/cancel (idempotent).
func (c *FleetClient) Cancel(ctx context.Context, runID, reason string) error {
	body, _ := json.Marshal(map[string]any{"reason": reason})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/fleet/jobs/"+runID+"/cancel", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fleet cancel request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fleet cancel call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("fleet cancel status %d", resp.StatusCode)
	}
	return nil
}
