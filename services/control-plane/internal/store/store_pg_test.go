package store

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
)

type pgxTx = pgx.Tx

// ed25519ToPEM encodes an ed25519 private key as PKCS#8 PEM like
// `openssl genpkey -algorithm ed25519`.
func ed25519ToPEM(priv ed25519.PrivateKey) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// testStore opens a store against TEST_PG_DSN, or skips the test.
func testStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping store test")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	if err := st.Migrate(ctx, "../../migrations"); err != nil {
		st.Close()
		t.Fatalf("migrate: %v", err)
	}
	return st, func() { st.Close() }
}

func TestAppendEventsChainAndOutbox(t *testing.T) {
	st, done := testStore(t)
	defer done()
	ctx := context.Background()

	ev1, err := domain.NewEvent("org_test", domain.AggregateRef{Type: string(domain.AggIntent), ID: "int_test"}, "intent.declared", "", "", domain.EventActor{Kind: "agent", ID: "t"}, map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	ev2, _ := domain.NewEvent("org_test", domain.AggregateRef{Type: string(domain.AggLease), ID: "lease_test"}, "lease.granted", ev1.ID, "", domain.EventActor{Kind: "agent", ID: "t"}, map[string]any{"n": 2})
	before, _ := st.MaxSeq(ctx)
	if err := st.AppendEvents(ctx, []*domain.Event{ev1, ev2}); err != nil {
		t.Fatalf("append: %v", err)
	}
	after, _ := st.MaxSeq(ctx)
	if after != before+2 {
		t.Fatalf("seq advanced %d -> %d, want +2", before, after)
	}
	if ev1.Seq != before+1 || ev2.Seq != before+2 {
		t.Fatalf("stamped seqs wrong: %d,%d", ev1.Seq, ev2.Seq)
	}
	if ev2.PrevHash != ev1.EntryHash {
		t.Fatal("prev_hash must chain")
	}
	var outbox int
	if err := st.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ctrl.outbox WHERE event_id = ANY($1)`, []string{ev1.ID, ev2.ID},
	).Scan(&outbox); err != nil || outbox != 2 {
		t.Fatalf("outbox rows=%d err=%v", outbox, err)
	}
}

func TestLedgerAppendOnlyTrigger(t *testing.T) {
	st, done := testStore(t)
	defer done()
	ctx := context.Background()
	ev, _ := domain.NewEvent("org_test", domain.AggregateRef{Type: string(domain.AggIntent), ID: "int_immutable"}, "intent.declared", "", "", domain.EventActor{Kind: "agent", ID: "t"}, map[string]any{"x": 1})
	if err := st.AppendEvents(ctx, []*domain.Event{ev}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE ctrl.ledger SET type='hacked' WHERE id=$1`, ev.ID); err == nil {
		t.Fatal("I-07 violated: ledger UPDATE succeeded")
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM ctrl.ledger WHERE id=$1`, ev.ID); err == nil {
		t.Fatal("I-07 violated: ledger DELETE succeeded")
	}
}

func TestCommandIdempotentReplay(t *testing.T) {
	st, done := testStore(t)
	defer done()
	ctx := context.Background()
	key := fmt.Sprintf("idem_%d", time.Now().UnixNano())
	hash := "sha256:aa"
	cached, err := st.LookupCommand(ctx, "org_test", "ep", key, hash)
	if err != nil || cached != nil {
		t.Fatalf("first lookup must miss: %v %v", cached, err)
	}
	err = st.ExecTx(ctx, func(tx pgxTx) error {
		return RecordCommandTx(ctx, tx, "org_test", "ep", key, hash, 200, []byte(`{"ok":true}`))
	})
	if err != nil {
		t.Fatal(err)
	}
	cached, err = st.LookupCommand(ctx, "org_test", "ep", key, hash)
	if err != nil || cached == nil || cached.ResponseCode != 200 {
		t.Fatalf("replay lookup failed: %v %v", cached, err)
	}
	if _, err := st.LookupCommand(ctx, "org_test", "ep", key, "sha256:bb"); err != domain.ErrConflict {
		t.Fatalf("hash mismatch must conflict, got %v", err)
	}
}

func TestUniqueAcceptedEvidencePerAttempt(t *testing.T) {
	st, done := testStore(t)
	defer done()
	ctx := context.Background()
	now := time.Now().UTC()
	runID := fmt.Sprintf("run_t%d", now.UnixNano())
	insert := func(status string) error {
		_, err := st.Pool.Exec(ctx,
			`INSERT INTO ctrl.evidence_records (tenant_id, id, seq, run_id, attempt, candidate_id, kind,
			   verdict, status, digests, inputs_hash, confidence, cost_millicents, created_at)
			 VALUES ('org_test',$1,0,$2,1,'cand_x','hermetic_build','pass',$3,'[]'::jsonb,'sha256:x',0.9,0,now())`,
			fmt.Sprintf("%s_%s", runID, status), runID, status)
		return err
	}
	if err := insert("accepted"); err != nil {
		t.Fatal(err)
	}
	if err := insert("accepted"); err == nil {
		t.Fatal("I-03 violated: second accepted evidence for (run_id,attempt)")
	}
	if err := insert("proposed"); err != nil {
		t.Fatalf("proposed alongside accepted must be allowed: %v", err)
	}
}

func TestCheckpointSigningRoundtrip(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := ed25519ToPEM(priv)
	path := t.TempDir() + "/key.pem"
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := LoadSigningKey(path)
	if err != nil {
		t.Fatalf("load signing key: %v", err)
	}
	sig := signer.SignCheckpoint(10000, "sha256:ab")
	if !signer.VerifyCheckpoint(10000, "sha256:ab", sig) {
		t.Fatal("checkpoint signature must verify")
	}
	if signer.VerifyCheckpoint(10001, "sha256:ab", sig) {
		t.Fatal("signature over different seq must fail")
	}
}

func TestProcessedEventsDedupe(t *testing.T) {
	st, done := testStore(t)
	defer done()
	ctx := context.Background()
	eventID := fmt.Sprintf("evt_dedupe_%d", time.Now().UnixNano())
	err := st.ExecTx(ctx, func(tx pgx.Tx) error {
		first, err := MarkProcessedTx(ctx, tx, "scheduler", eventID)
		if err != nil {
			return err
		}
		if !first {
			t.Fatal("first insert must be new")
		}
		second, err := MarkProcessedTx(ctx, tx, "scheduler", eventID)
		if err != nil {
			return err
		}
		if second {
			t.Fatal("I-12 violated: duplicate insert reported fresh inside same tx")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
