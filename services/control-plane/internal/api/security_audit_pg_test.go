package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"sauron.dev/sauron/control-plane/internal/audit"
)

// TestSecurityAuditMarkerExactlyOnce drives the ingest sig-failed marker
// seam against Postgres: each triggering request audits EXACTLY once, and
// redelivered markers (same nonce-suffixed id) collapse on the I-12
// processed guard without a second row. Skips without TEST_PG_DSN.
func TestSecurityAuditMarkerExactlyOnce(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping PG-backed audit test")
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
	srv := NewServer(cfg, st, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	before := countSecurityAudit(t, st, audit.KindWebhookSignatureFailed)
	guid := "guid-" + idemKey("sigfail") + ".sigfailed.01HZZZZMARKERTEST"

	if code := postMarker(t, ts, cfg, guid); code != http.StatusAccepted {
		t.Fatalf("first marker = %d, want 202", code)
	}
	if code := postMarker(t, ts, cfg, guid); code != http.StatusOK {
		t.Fatalf("marker replay = %d, want 200", code)
	}
	after := countSecurityAudit(t, st, audit.KindWebhookSignatureFailed)
	if after != before+1 {
		t.Fatalf("audit rows delta = %d, want exactly 1", after-before)
	}
}
