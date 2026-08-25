package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// seedCandidateWithIntent creates one live validating candidate through the
// real REST path so replans hit genuine projections.

// TestRevalidateBudgetCap drives POST /v1/candidates/{id}/revalidate against
// Postgres: 202 under cap with a fresh plan_id each call, then 409
// rerun_budget_exhausted at the third attempt (ruling #2: cap 2).
func TestRevalidateBudgetCap(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping PG-backed revalidate test")
	}
	st, err := openStore(dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(migrateCtx(), "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := webhookConfig()
	ts := httptest.NewServer(NewServer(cfg, st, nil).Handler())
	defer ts.Close()

	repo := fmt.Sprintf("acme/reval-%d-%s", os.Getpid(), strings.ToLower(idemKey("repo")))
	intentHeaders := map[string]string{"Idempotency-Key": idemKey("intent")}
	resp, close1 := authedJSON(ts, http.MethodPost, "/v1/change-intents", cfg.AdminToken, map[string]any{
		"goal":                "revalidate budget probe",
		"repository":          repo,
		"base":                "main",
		"expected_surfaces":   []string{"services/**"},
		"acceptance_criteria": []string{"pass"},
		"risk":                "medium",
	}, intentHeaders)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create intent = %d", resp.StatusCode)
	}
	var grant map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&grant)
	close1()

	candHeaders := map[string]string{"Idempotency-Key": idemKey("cand")}
	submitted := fmt.Sprintf(`{"patch_ref":"bundle:x","head_sha":"%s","base_sha":"%s","changed_paths":["a.go"]}`,
		revA, revB)
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/v1/change-intents/"+grant["intent_id"].(string)+"/candidates", strings.NewReader(submitted))
	req.Header.Set("Authorization", "Bearer "+cfg.AdminToken)
	for k, v := range candHeaders {
		req.Header.Set(k, v)
	}
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var accepted struct {
		CandidateID string `json:"candidate_id"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&accepted)
	resp2.Body.Close()
	if accepted.CandidateID == "" {
		t.Fatal("no candidate registered")
	}

	revalidate := func(idem string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost,
			ts.URL+"/v1/candidates/"+accepted.CandidateID+"/revalidate", strings.NewReader(`{"reason":"check_run.rerequested"}`))
		req.Header.Set("Authorization", "Bearer "+cfg.AdminToken)
		req.Header.Set("Idempotency-Key", idem)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}

	firstCode, firstBody := revalidate(idemKey("rv1"))
	if firstCode != http.StatusAccepted || firstBody["plan_id"] == nil {
		t.Fatalf("first revalidate = %d %v want 202+plan_id", firstCode, firstBody)
	}
	firstPlan := firstBody["plan_id"].(string)

	secondCode, secondBody := revalidate(idemKey("rv2"))
	if secondCode != http.StatusAccepted || secondBody["plan_id"] == nil {
		t.Fatalf("second revalidate = %d %v want 202", secondCode, secondBody)
	}
	if secondBody["plan_id"].(string) == firstPlan {
		t.Fatal("each revalidation must mint a FRESH plan_id")
	}

	thirdCode, thirdBody := revalidate(idemKey("rv3"))
	if thirdCode != http.StatusConflict {
		t.Fatalf("third revalidate = %d want 409", thirdCode)
	}
	errObj, _ := thirdBody["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "conflict_state" {
		t.Fatalf("conflict envelope broken: %v", thirdBody)
	}
	details, _ := errObj["details"].(map[string]any)
	if details == nil || details["reason"] != "rerun_budget_exhausted" {
		t.Fatalf("details.reason must be rerun_budget_exhausted, got %v", details)
	}
}

// TestPoliciesActiveEndpoint serves the ctrl.policies projection readonly.
func TestPoliciesActiveEndpoint(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping PG-backed policies test")
	}
	st, err := openStore(dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(migrateCtx(), "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := webhookConfig()
	ts := httptest.NewServer(NewServer(cfg, st, nil).Handler())
	defer ts.Close()

	polID := fmt.Sprintf("pol_test_%d_%s", os.Getpid(), strings.ToLower(idemKey("pol")))
	if _, err := st.Pool.Exec(migrateCtx(),
		`INSERT INTO ctrl.policies (tenant_id, id, version, status, body, activated_by, activated_at, seq)
		 VALUES ($1,$2,3,'active','{"min_selection_confidence":0.98}'::jsonb,'test',now(),99),
		        ($1,$2,2,'retired','{}'::jsonb,'test',now(),98)`,
		cfg.TenantID, polID); err != nil {
		t.Fatal(err)
	}

	get := func(path string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+cfg.AdminToken)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}

	code, active := get("/v1/policies/active")
	if code != http.StatusOK {
		t.Fatalf("active = %d", code)
	}
	rows := active["policies"].([]any)
	foundActive := false
	for _, row := range rows {
		m := row.(map[string]any)
		if m["policy_id"] == polID {
			if m["status"] != "active" || m["version"].(float64) != 3 {
				t.Fatalf("active view must serve the active version only: %v", m)
			}
			foundActive = true
		}
	}
	if !foundActive {
		t.Fatal("active policy missing from /v1/policies/active")
	}

	code, history := get("/v1/policies/active?history=1")
	if code != http.StatusOK {
		t.Fatalf("history = %d", code)
	}
	versions := map[float64]bool{}
	for _, row := range history["policies"].([]any) {
		m := row.(map[string]any)
		if m["policy_id"] == polID {
			versions[m["version"].(float64)] = true
		}
	}
	if !versions[2] || !versions[3] {
		t.Fatalf("history must include retired versions, got %v", versions)
	}
}
