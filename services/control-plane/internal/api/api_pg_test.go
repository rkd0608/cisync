package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/store"
)

var idemCounter atomic.Int64

func idemKey(prefix string) string {
	return fmt.Sprintf("%s_%d_%d_%s", prefix, os.Getpid(), idemCounter.Add(1), strings.Repeat("k", 8))
}

func pgServer(t *testing.T) (*httptest.Server, *store.Store, *config.Config) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping PG-backed api test")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	if err := st.Migrate(ctx, "../../migrations"); err != nil {
		st.Close()
		t.Fatalf("migrate: %v", err)
	}
	cfg := testConfig()
	srv := NewServer(cfg, st, nil)
	ts := httptest.NewServer(srv.Handler())
	return ts, st, cfg
}

func authedJSON(ts *httptest.Server, method, path, token string, body map[string]any, headers map[string]string) (*http.Response, func()) {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, ts.URL+path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	return resp, func() { resp.Body.Close() }
}

func TestCreateIntentFlowAndIdempotentReplay(t *testing.T) {
	ts, st, cfg := pgServer(t)
	defer ts.Close()
	defer st.Close()

	headers := map[string]string{"Idempotency-Key": idemKey("intent")}
	body := map[string]any{
		"goal":                "add idempotency keys to checkout",
		"repository":          fmt.Sprintf("acme/payments-%d-%s", os.Getpid(), idemKey("repo")),
		"base":                "main",
		"expected_surfaces":   []string{"services/checkout/**"},
		"acceptance_criteria": []string{"all tests pass"},
		"risk":                "high",
	}
	resp, close1 := authedJSON(ts, http.MethodPost, "/v1/change-intents", cfg.AdminToken, body, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create intent = %d", resp.StatusCode)
	}
	var grant map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&grant); err != nil {
		t.Fatal(err)
	}
	close1()
	intentID, _ := grant["intent_id"].(string)
	leaseID, _ := grant["lease_id"].(string)
	if !strings.HasPrefix(intentID, "int_") || !strings.HasPrefix(leaseID, "lease_") {
		t.Fatalf("bad ids: %v %v", intentID, leaseID)
	}

	resp2, close2 := authedJSON(ts, http.MethodPost, "/v1/change-intents", cfg.AdminToken, body, headers)
	defer close2()
	var replayRaw bytes.Buffer
	if _, err := replayRaw.ReadFrom(resp2.Body); err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK || replayRaw.String() == "" {
		t.Fatalf("replay status=%d", resp2.StatusCode)
	}
	var replayGrant map[string]any
	if err := json.Unmarshal(replayRaw.Bytes(), &replayGrant); err != nil {
		t.Fatal(err)
	}
	if replayGrant["intent_id"] != grant["intent_id"] || replayGrant["lease_id"] != grant["lease_id"] {
		t.Fatalf("replay returned different ids: %v vs %v", replayGrant["intent_id"], grant["intent_id"])
	}
	if len(replayRaw.Bytes()) != len(mustJSON(grant)) && !jsonEqual(replayRaw.Bytes(), grant) {
		t.Fatal("replay body must equal original response")
	}
}

func jsonEqual(raw []byte, want map[string]any) bool {
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		return false
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	return string(a) == string(b)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestSubmitCandidatePlansRuns(t *testing.T) {
	ts, st, cfg := pgServer(t)
	defer ts.Close()
	defer st.Close()

	repo := fmt.Sprintf("acme/plan-%d-%s", os.Getpid(), idemKey("repo"))
	createHeaders := map[string]string{"Idempotency-Key": idemKey("ci")}
	body := map[string]any{
		"goal": "g", "repository": repo, "base": "main",
		"expected_surfaces": []string{"services/x/**"}, "risk": "medium",
	}
	resp, c1 := authedJSON(ts, http.MethodPost, "/v1/change-intents", cfg.AdminToken, body, createHeaders)
	if resp.StatusCode != 200 {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	var grant map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&grant)
	c1()
	intentID := grant["intent_id"].(string)

	submitHeaders := map[string]string{"Idempotency-Key": idemKey("sc")}
	submitBody := map[string]any{
		"patch_ref": "bundle://x",
		"head_sha":  deterministicSHA(1),
		"base_sha":  deterministicSHA(2),
	}
	sresp, c2 := authedJSON(ts, http.MethodPost, "/v1/change-intents/"+intentID+"/candidates", cfg.AdminToken, submitBody, submitHeaders)
	if sresp.StatusCode != 201 {
		t.Fatalf("submit = %d", sresp.StatusCode)
	}
	var accepted struct {
		CandidateID string `json:"candidate_id"`
		PlanSummary struct {
			Tiers []struct {
				Tier      int      `json:"tier"`
				Jobs      []string `json:"jobs"`
				Rationale string   `json:"rationale"`
			} `json:"tiers"`
			Deferred []string `json:"deferred"`
		} `json:"plan_summary"`
		LeaseID string `json:"lease_id"`
	}
	if err := json.NewDecoder(sresp.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	c2()
	if !strings.HasPrefix(accepted.CandidateID, "cand_") || len(accepted.PlanSummary.Tiers) < 2 {
		t.Fatalf("plan summary incomplete: %+v", accepted)
	}
	if accepted.LeaseID == "" {
		t.Fatal("lease_id required in CandidateAccepted")
	}

	dupResp, c3 := authedJSON(ts, http.MethodPost, "/v1/change-intents/"+intentID+"/candidates", cfg.AdminToken, submitBody, map[string]string{"Idempotency-Key": idemKey("dup")})
	defer c3()
	if dupResp.StatusCode != 409 {
		t.Fatalf("duplicate head_sha = %d want 409", dupResp.StatusCode)
	}
}

func TestUniform404CrossTenant(t *testing.T) {
	ts, st, cfg := pgServer(t)
	defer ts.Close()
	defer st.Close()
	ctx := context.Background()

	otherTenantIntent := "int_" + uniqueSuffix()
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ctrl.intents (tenant_id, id, seq, state, goal, repo, base_ref, base_snapshot,
		   owned_surfaces, risk_class, origin, resolved_policy, compute_budget, created_at)
		 VALUES ('org_other_tenant', $1, 0, 'exploring', 'secret', 'acme/hidden', 'main', 'main@deadbeef',
		   ARRAY['services/**'], 'high', 'agent_api', '{"policy_id":"pol_x","policy_version":1}'::jsonb,
		   '{"cpu_minutes":1,"environment_minutes":1,"repair_attempts":1}'::jsonb, now())`, otherTenantIntent)
	if err != nil {
		t.Fatalf("seed other-tenant intent: %v", err)
	}

	respMissing, c1 := authedJSON(ts, http.MethodGet, "/v1/change-intents/int_nonexistent00000000000000", cfg.AdminToken, nil, nil)
	defer c1()
	respCross, c2 := authedJSON(ts, http.MethodGet, "/v1/change-intents/"+otherTenantIntent, cfg.AdminToken, nil, nil)
	defer c2()

	if respMissing.StatusCode != 404 || respCross.StatusCode != 404 {
		t.Fatalf("want uniform 404: missing=%d cross=%d", respMissing.StatusCode, respCross.StatusCode)
	}
	mb, _ := io.ReadAll(respMissing.Body)
	cb, _ := io.ReadAll(respCross.Body)
	if string(mb) != string(cb) {
		t.Fatalf("cross-tenant 404 body differs:\n%s\n%s", mb, cb)
	}
}

func deterministicSHA(n byte) string {
	out := make([]byte, 40)
	for i := range out {
		out[i] = 'a' + (n+byte(i))%26
	}
	return string(out)
}

var uniqueCounter atomic.Int64

// uniqueSuffix builds a 26-char Crockford-base32-ish ULID-shaped id.
func uniqueSuffix() string {
	v := uniqueCounter.Add(1)
	mix := uint64(os.Getpid())<<32 ^ uint64(v)
	const digits = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	out := make([]byte, 26)
	for i := 25; i >= 0; i-- {
		out[i] = digits[mix%32]
		mix /= 32
		if mix == 0 {
			mix = 1
		}
	}
	return string(out)
}
