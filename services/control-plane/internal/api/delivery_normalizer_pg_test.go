package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/store"
)

// migrateCtx keeps fixture-table call sites terse.
func migrateCtx() context.Context { return context.Background() }

func openStore(dsn string) (*store.Store, error) { return store.Open(migrateCtx(), dsn) }

func candidateHeadState(ctx context.Context, st *store.Store, tenant, intentID, headSHA string) (bool, bool, error) {
	return store.CandidateHeadStateTx(ctx, st.Pool, tenant, intentID, headSHA)
}

// webhookConfig extends the shared test config with the wave-5 knobs.
func webhookConfig() *config.Config {
	cfg := testConfig()
	cfg.TrackedBaseBranches = []string{"main", "master"}
	cfg.RerunMaxPerCandidate = 2
	return cfg
}

func uniqueRepo() string {
	return fmt.Sprintf("acme/payments-%d-%s", os.Getpid(), strings.ToLower(idemKey("repo")))
}

// postDelivery signs and forwards one internal-protocols §1 envelope.
func postDelivery(t *testing.T, ts *httptest.Server, secret, extID, eventKind, repo string, payload []byte) int {
	t.Helper()
	env := map[string]any{
		"source":          "github",
		"ext_delivery_id": extID,
		"event_kind":      eventKind,
		"repo":            repo,
		"received_at":     "2026-08-24T12:00:00Z",
	}
	if payload != nil {
		env["payload"] = json.RawMessage(payload)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/internal/ctrl/deliveries", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", extID)
	req.Header.Set("X-Sauron-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../../tests/fixtures/github/" + name)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return raw
}

// normalizerEnv carries shared fixture-table state so scenario groups can
// live across two files without breaching the charter's 250-line cap.
type normalizerEnv struct {
	t        *testing.T
	ts       *httptest.Server
	st       *store.Store
	cfg      *config.Config
	tenant   string
	repo     string
	baseline int64
	guidN    int
	opened   []byte
	sync     []byte
}

func (e *normalizerEnv) guid() string {
	e.guidN++
	return fmt.Sprintf("norm-%d-%s-%04d", os.Getpid(), e.repo[strings.LastIndex(e.repo, "-")+1:], e.guidN)
}

// ledgerTypes counts ledger event types appended during THIS run only.
func (e *normalizerEnv) ledgerTypes(t *testing.T) map[string]int {
	t.Helper()
	events, _, err := e.st.TailEvents(migrateCtx(), e.tenant, e.baseline, nil, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, ev := range events {
		counts[ev.Type]++
	}
	return counts
}

func newNormalizerEnv(t *testing.T) *normalizerEnv {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping PG-backed normalizer table")
	}
	st, err := openStore(dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(migrateCtx(), "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := webhookConfig()
	ts := httptest.NewServer(NewServer(cfg, st, nil).Handler())
	t.Cleanup(ts.Close)
	baseline, err := st.MaxSeq(migrateCtx())
	if err != nil {
		t.Fatalf("max seq: %v", err)
	}
	return &normalizerEnv{
		t: t, ts: ts, st: st, cfg: cfg,
		tenant:   cfg.TenantID,
		repo:     uniqueRepo(),
		baseline: baseline,
		opened:   loadFixture(t, "pull_request.opened.json"),
		sync:     loadFixture(t, "pull_request.synchronize.json"),
	}
}

// TestNormalizerFixtureTable drives the §3.1 mapping table end-to-end over
// sanitized fixtures through signed HTTP deliveries against real Postgres.
// The revision-chain group lives here; cascade scenarios in the sibling file.
func TestNormalizerFixtureTable(t *testing.T) {
	e := newNormalizerEnv(t)
	runRevisionScenarios(t, e)
	runCascadeScenarios(t, e)
}

func runRevisionScenarios(t *testing.T, e *normalizerEnv) {
	code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "pull_request.opened", e.repo, e.opened)
	if code != http.StatusAccepted {
		t.Fatalf("opened delivery = %d", code)
	}
	intent, err := e.st.IntentForPR(migrateCtx(), e.tenant, e.repo, 7)
	if err != nil {
		t.Fatalf("synthetic intent missing: %v", err)
	}
	if intent.Declared.Origin != domain.OriginGitHubHook {
		t.Fatalf("origin=%q want github_webhook", intent.Declared.Origin)
	}
	live, known, err := candidateHeadState(migrateCtx(), e.st, e.tenant, intent.ID,
		"1111111111111111111111111111111111111111")
	if err != nil || !known || !live {
		t.Fatalf("candidate not live after opened: live=%v known=%v err=%v", live, known, err)
	}

	// Same delivery replay is idempotent (I-12 inside the effect tx).
	id := e.guid()
	if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, id, "pull_request.opened", e.repo, e.opened); code != http.StatusAccepted {
		t.Fatalf("first = %d", code)
	}
	before := e.ledgerTypes(t)["candidate.submitted"]
	if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, id, "pull_request.opened", e.repo, e.opened); code != http.StatusOK {
		t.Fatalf("replay must be 200 per internal-protocols §1, got %d", code)
	}
	if after := e.ledgerTypes(t)["candidate.submitted"]; after != before {
		t.Fatalf("replay appended candidates")
	}

	// synchronize supersedes prior revision.
	beforeSuperseded := e.ledgerTypes(t)["candidate.superseded"]
	if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "pull_request.synchronize", e.repo, e.sync); code != http.StatusAccepted {
		t.Fatalf("synchronize = %d", code)
	}
	if after := e.ledgerTypes(t)["candidate.superseded"]; after-beforeSuperseded == 0 {
		t.Fatal("no candidate.superseded appended")
	}
	live, _, err = candidateHeadState(migrateCtx(), e.st, e.tenant, intent.ID,
		"3333333333333333333333333333333333333333")
	if err != nil || !live {
		t.Fatalf("new head must be live: %v", err)
	}

	// Same-head redelivery is a no-op replay (duplicate_sha semantics).
	beforeSubmits := e.ledgerTypes(t)["candidate.submitted"]
	if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "pull_request.synchronize", e.repo, e.sync); code != http.StatusAccepted {
		t.Fatalf("redelivery = %d", code)
	}
	if after := e.ledgerTypes(t)["candidate.submitted"]; after != beforeSubmits {
		t.Fatal("same-head redelivery submitted a duplicate candidate")
	}

	// Out-of-order stale head is record-only — never supersedes newer work.
	stalePayload := strings.Replace(string(e.sync), "3333333333333333333333333333333333333333",
		"1111111111111111111111111111111111111111", 1)
	beforeSuperseded = e.ledgerTypes(t)["candidate.superseded"]
	if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "pull_request.synchronize", e.repo, []byte(stalePayload)); code != http.StatusAccepted {
		t.Fatalf("stale delivery = %d", code)
	}
	if after := e.ledgerTypes(t)["candidate.superseded"]; after != beforeSuperseded {
		t.Fatal("stale head must not supersede newer work")
	}
}
