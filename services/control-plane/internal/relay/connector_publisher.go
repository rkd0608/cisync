package relay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sauron.dev/sauron/control-plane/internal/store"
)

// ConnectorPublisher pushes rendered decisions to the github-connector
// (internal-protocols.md §4). An empty connectorURL disables publishing —
// compose environments without the connector stay fully functional.
type ConnectorPublisher struct {
	store        *store.Store
	connectorURL string
	secret       string
	detailsURL   string
	http         *http.Client
}

// NewConnectorPublisher constructs the §4 push consumer.
func NewConnectorPublisher(st *store.Store, connectorURL, secret, detailsURL string) *ConnectorPublisher {
	return &ConnectorPublisher{
		store:        st,
		connectorURL: connectorURL,
		secret:       secret,
		detailsURL:   detailsURL,
		http:         &http.Client{Timeout: 10 * time.Second},
	}
}

// ConsumeRendered implements the relay Handler contract for
// decision.rendered events: it resolves repo/head_sha from the candidate and
// POSTs the HMAC-signed envelope to the connector.
func (p *ConnectorPublisher) ConsumeRendered(ctx context.Context, item store.OutboxItem) error {
	if p.connectorURL == "" {
		return nil // connector not configured; publishing disabled
	}
	events, _, err := p.store.TailEvents(ctx, item.TenantID, 0, []string{"decision.rendered"}, item.AggType+":"+item.AggID, 1)
	if err != nil || len(events) == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("connector publisher: decision %s not found", item.AggID)
	}
	payload := events[0].Payload
	candidateID, _ := payload["subject"].(map[string]any)["id"].(string)
	if candidateID == "" {
		return fmt.Errorf("connector publisher: decision %s has no candidate subject", item.AggID)
	}
	cand, err := p.store.GetCandidate(ctx, item.TenantID, candidateID)
	if err != nil {
		return fmt.Errorf("connector publisher: load candidate %s: %w", candidateID, err)
	}
	intent, err := p.store.GetIntent(ctx, item.TenantID, cand.IntentID)
	if err != nil {
		return fmt.Errorf("connector publisher: load intent %s: %w", cand.IntentID, err)
	}

	verb, _ := payload["verb"].(string)
	confidence, _ := payload["confidence"].(float64)
	policyRef := mapPolicyRef(payload["policy"])
	envelope := map[string]any{
		"decision_id":  item.AggID,
		"candidate_id": candidateID,
		"repo":         intent.Declared.Repo,
		"head_sha":     cand.HeadSHA,
		"verb":         verb,
		"confidence":   confidence,
		"policy":       policyRef,
		"summary":      summaryOf(payload),
		"rendered_at":  events[0].OccurredAt.UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("connector publisher: marshal envelope: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.connectorURL+"/internal/connector/decisions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", item.AggID)
	req.Header.Set("X-Sauron-Signature", signBody(p.secret, body))
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("connector publisher: post: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 500:
		return fmt.Errorf("connector publisher: status %d", resp.StatusCode)
	case resp.StatusCode >= 400:
		// Permanent client rejection: log-and-drop rather than retry-loop;
		// the decision remains authoritative in the ledger.
		logf("connector rejected decision %s with %d; dropping", item.AggID, resp.StatusCode)
	}
	return nil
}

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func mapPolicyRef(raw any) map[string]any {
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func summaryOf(payload map[string]any) string {
	if expl, ok := payload["explanation"].(map[string]any); ok {
		if s, ok := expl["summary"].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
