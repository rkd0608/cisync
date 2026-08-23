package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/store"
)

// Shared helpers for the wave-3 conformance regression tests
// (conformance_pg_test.go). All PG-backed; the suites skip without
// TEST_PG_DSN so hermetic runs stay green.

func createIntentRaw(t *testing.T, ts *httptest.Server, token, repo string) []byte {
	t.Helper()
	body := map[string]any{
		"goal": "correlation probe", "repository": repo, "base": "main",
		"expected_surfaces": []string{"services/**"}, "risk": "medium",
	}
	resp, closeFn := authedJSON(ts, http.MethodPost, "/v1/change-intents", token, body,
		map[string]string{"Idempotency-Key": idemKey("corr")})
	defer closeFn()
	raw := readAll(t, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d: %s", resp.StatusCode, raw)
	}
	return raw
}

func readAll(t *testing.T, r interface{ Read([]byte) (int, error) }) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type rawResponse struct {
	status int
	raw    []byte
}

func submitCandidateRaw(t *testing.T, ts *httptest.Server, token, intentID, headSHA, baseSHA, tag string) rawResponse {
	t.Helper()
	body := map[string]any{
		"patch_ref": "bundle://" + tag, "head_sha": headSHA, "base_sha": baseSHA,
	}
	resp, closeFn := authedJSON(ts, http.MethodPost, "/v1/change-intents/"+intentID+"/candidates", token, body,
		map[string]string{"Idempotency-Key": idemKey("bm-" + tag)})
	defer closeFn()
	return rawResponse{status: resp.StatusCode, raw: readAll(t, resp.Body)}
}

func createReleasedIntentLease(t *testing.T, ts *httptest.Server, cfg *config.Config) (string, string) {
	t.Helper()
	body := map[string]any{
		"goal": "lease terminal probe", "repository": fmt.Sprintf("acme/lease-ttl-%d-%s", os.Getpid(), uniqueSuffix()),
		"base": "main", "expected_surfaces": []string{"services/**"}, "risk": "low",
	}
	resp, closeFn := authedJSON(ts, http.MethodPost, "/v1/change-intents", cfg.AdminToken, body,
		map[string]string{"Idempotency-Key": idemKey("lt")})
	defer closeFn()
	raw := readAll(t, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d: %s", resp.StatusCode, raw)
	}
	intentID := stringFromJSON(t, raw, "intent_id")
	leaseID := stringFromJSON(t, raw, "lease_id")

	delResp, delClose := authedJSON(ts, http.MethodDelete, "/v1/leases/"+leaseID, cfg.AdminToken, nil, nil)
	delClose()
	if delResp.StatusCode != http.StatusNoContent && delResp.StatusCode != http.StatusOK {
		t.Fatalf("release = %d", delResp.StatusCode)
	}
	return intentID, leaseID
}

func stringFromJSON(t *testing.T, raw []byte, key string) string {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("decode json: %v (%s)", err, raw)
	}
	v, _ := parsed[key].(string)
	return v
}

func plannedInputsHash(st *store.Store, cfg *config.Config, candidateID string) string {
	hash, err := st.InputsHashForCandidate(context.Background(), cfg.TenantID, candidateID)
	if err != nil {
		return ""
	}
	return hash
}
