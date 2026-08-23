package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/domain"
)

func testConfig() *config.Config {
	return &config.Config{
		PGDSN:             "unused",
		Addr:              ":0",
		FleetURL:          "http://localhost:8082",
		AdminToken:        "test_admin_token_0123456789",
		WebhookSecret:     "test_webhook_secret",
		LedgerKeyFile:     "",
		TenantID:          "org_default",
		RelayBatchSize:    10,
		RelayPollInterval: 500000000,
		TickInterval:      1000000000,
		ReconcileInterval: 30000000000,
		DefaultLeaseTTL:   1500000000000,
		RateLimitPerMin:   120,
	}
}

// TestAuthRejectsBadBearer runs without a database: the auth middleware must
// reject before any store access.
func TestAuthRejectsBadBearer(t *testing.T) {
	srv := NewServer(testConfig(), nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/change-intents", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing bearer = %d, want 401", resp.StatusCode)
	}
	var envelope ErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "unauthorized" {
		t.Fatalf("code=%s want unauthorized", envelope.Error.Code)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/change-intents", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong_token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad bearer = %d, want 401", resp2.StatusCode)
	}
}

func TestHealthzOpenAndMetricsRender(t *testing.T) {
	srv := NewServer(testConfig(), nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v %d", err, resp.StatusCode)
	}
	resp.Body.Close()
	m, err := http.Get(ts.URL + "/metrics")
	if err != nil || m.StatusCode != 200 {
		t.Fatalf("metrics: %v %d", err, m.StatusCode)
	}
	ct := m.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("metrics content-type %q", ct)
	}
	m.Body.Close()
}

func TestDeliveryHMACFailure(t *testing.T) {
	srv := NewServer(testConfig(), nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	body := `{"source":"github","ext_delivery_id":"d-1","event_kind":"push","repo":"a/b","received_at":"2026-01-01T00:00:00Z","payload":{}}`

	mac := hmac.New(sha256.New, []byte("wrong_secret"))
	mac.Write([]byte(body))
	badSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/internal/ctrl/deliveries", strings.NewReader(body))
	req.Header.Set("X-Sauron-Signature", badSig)
	req.Header.Set("Idempotency-Key", "delivery-12345678")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad HMAC = %d, want 401", resp.StatusCode)
	}
}

func TestUniformNotFoundShape(t *testing.T) {
	srv := NewServer(testConfig(), nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path = %d want 404", resp.StatusCode)
	}
	var envelope ErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "not_found" {
		t.Fatalf("code=%s want not_found", envelope.Error.Code)
	}
}

func TestIntentGrantJSONShapeMatchesOpenAPI(t *testing.T) {
	now := time.Now().UTC()
	intent := domain.NewIntent("int_test", "org_test", domain.IntentDeclared{
		Goal: "g", Repo: "acme/payments", BaseRef: "main",
		BaseSnapshot:   "main@b734e",
		OwnedSurfaces:  []string{"services/**"},
		RiskClass:      domain.RiskHigh,
		Origin:         domain.OriginAgentAPI,
		ResolvedPolicy: domain.PolicyRef{PolicyID: "pol_default", Version: 1},
	}, now)
	lease := domain.NewLease("lease_test", "org_test", "int_test",
		domain.LeaseScope{Kind: domain.ScopeChangeScope},
		"agent:org_test", domain.BudgetValues{CPUMinutes: 120, EnvironmentMinutes: 30, RepairAttempts: 2},
		time.Minute, []string{"hermetic_build"}, now)
	conflicts := []domain.ConflictRef{{IntentID: "int_other", Relation: "overlapping", Owner: "acme/payments", Recommendation: "coordinate"}}
	grant := buildIntentGrant(intent, lease, conflicts, []string{"infrastructure/prod/**"}, nil, nil)
	raw, _ := json.Marshal(grant)
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	required := []string{
		"intent_id", "lease_id", "base_snapshot", "worktree_or_branch",
		"allowed_paths", "prohibited_paths", "conflicts", "required_evidence",
		"compute_budget", "queue_position", "eta_seconds",
	}
	for _, k := range required {
		if _, ok := parsed[k]; !ok {
			t.Errorf("IntentGrant missing required key %q", k)
		}
	}
	budget, ok := parsed["compute_budget"].(map[string]any)
	if !ok {
		t.Fatal("compute_budget must be an object")
	}
	for _, k := range []string{"cpu_minutes", "environment_minutes", "repair_attempts"} {
		if _, ok := budget[k]; !ok {
			t.Errorf("compute_budget missing %q", k)
		}
	}
}
