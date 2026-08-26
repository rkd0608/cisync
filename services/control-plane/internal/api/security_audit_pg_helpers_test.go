package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cisync.dev/cisync/control-plane/internal/audit"
	"cisync.dev/cisync/control-plane/internal/config"
	"cisync.dev/cisync/control-plane/internal/store"
)

// countSecurityAudit reads the number of persisted security_audit rows for
// one event kind.
func countSecurityAudit(t *testing.T, st *store.Store, kind audit.Kind) int64 {
	t.Helper()
	var n int64
	err := st.Pool.QueryRow(migrateCtx(),
		`SELECT count(*) FROM ctrl.security_audit WHERE event_kind=$1`, string(kind)).Scan(&n)
	if err != nil {
		t.Fatalf("count security audit: %v", err)
	}
	return n
}

// postMarker posts one sig-failed quarantine marker with a valid HMAC and
// returns the HTTP status.
func postMarker(t *testing.T, ts *httptest.Server, cfg *config.Config, extID string) int {
	t.Helper()
	body := `{"source":"github","ext_delivery_id":"` + extID + `","event_kind":"sig_failed",` +
		`"repo":"acme/payments","received_at":"2026-01-01T00:00:00Z","payload":{},` +
		`"quarantine_reason":"signature_verification_failed"}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/internal/ctrl/deliveries", strings.NewReader(body))
	req.Header.Set("X-CISync-Signature", "sha256="+signWith(cfg.WebhookSecret, []byte(body)))
	req.Header.Set("Idempotency-Key", extID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
