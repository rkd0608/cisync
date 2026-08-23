package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestProcessedKeysLookup covers the bulk replay pre-check used by the
// completion feed: already-consumed keys must be found, unknown keys must
// not appear, and the check itself must never mark anything processed.
// PG-backed; skips without TEST_PG_DSN.
func TestProcessedKeysLookup(t *testing.T) {
	st, done := testStore(t)
	defer done()
	ctx := context.Background()

	consumer := "completion_ingest_test"
	// Run-unique keys: the dev DB persists, so a previous execution of this
	// test must not leak state into the next one.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	keys := []string{
		"fleet:test-run-1:" + suffix,
		"fleet:test-run-2:" + suffix,
		"fleet:test-run-3:" + suffix,
	}

	err := st.ExecTx(ctx, func(tx pgx.Tx) error {
		for _, k := range keys[:2] {
			if _, err := MarkProcessedTx(ctx, tx, consumer, k); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed processed rows: %v", err)
	}

	found, err := st.ProcessedKeys(ctx, consumer, keys)
	if err != nil {
		t.Fatalf("ProcessedKeys: %v", err)
	}
	if !found[keys[0]] || !found[keys[1]] {
		t.Fatalf("consumed keys missing from lookup: %v", found)
	}
	if found[keys[2]] {
		t.Fatalf("unknown key reported as processed: %v", found)
	}

	// The pre-check must be side-effect free: a later real consume still
	// reports first=true for the unmarked key.
	err = st.ExecTx(ctx, func(tx pgx.Tx) error {
		first, err := MarkProcessedTx(ctx, tx, consumer, keys[2])
		if err != nil {
			return err
		}
		if !first {
			t.Fatal("pre-check mutated processed_events; I-12 gate broken")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("post-check consume: %v", err)
	}
}
