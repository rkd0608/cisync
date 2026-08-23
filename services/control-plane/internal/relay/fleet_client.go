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

// CompletedJob is one accepted terminal job from the completion feed.
type CompletedJob struct {
	RunID           string   `json:"run_id"`
	Attempt         int      `json:"attempt"`
	FenceToken      int64    `json:"fence_token"`
	Tier            int      `json:"tier"`
	Pool            string   `json:"pool"`
	Status          string   `json:"status"`
	LogsDigest      string   `json:"logs_digest"`
	LogsExcerpt     string   `json:"logs_excerpt,omitempty"`
	ArtifactDigests []string `json:"artifact_digests"`
	DurationMS      int64    `json:"duration_ms"`
	CostMillicents  int64    `json:"actual_cost_millicents"`
	Classification  string   `json:"classification,omitempty"`
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

// EnqueueRequest is the control-plane → fleet job push (§2).
type EnqueueRequest struct {
	RunID   string         `json:"run_id"`
	Attempt int            `json:"attempt"`
	Tier    int            `json:"tier"`
	Pool    string         `json:"pool"`
	JobSpec map[string]any `json:"job_spec"`
}

// Enqueue POSTs /internal/fleet/jobs to register a claimable execution job;
// duplicate run_ids are an accepted idempotent replay.
func (c *FleetClient) Enqueue(ctx context.Context, req EnqueueRequest) error {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/fleet/jobs", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fleet enqueue request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("fleet enqueue call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("fleet enqueue status %d", resp.StatusCode)
	}
	return nil
}

// Completed GETs /internal/fleet/jobs/completed — the accepted terminal jobs
// control-plane ingests to drive evidence/failure/decision effects.
func (c *FleetClient) Completed(ctx context.Context, limit int) ([]CompletedJob, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/internal/fleet/jobs/completed?limit=%d", c.baseURL, limit), nil)
	if err != nil {
		return nil, fmt.Errorf("fleet completed request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fleet completed call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet completed status %d", resp.StatusCode)
	}
	var parsed struct {
		Jobs []CompletedJob `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("fleet completed decode: %w", err)
	}
	return parsed.Jobs, nil
}
