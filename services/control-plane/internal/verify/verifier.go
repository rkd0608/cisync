package verify

import (
	"context"
	"crypto/ed25519"
	"fmt"

	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/store"
)

// Report summarizes one chain-verification pass.
type Report struct {
	Entries     int
	Checkpoints int
	OK          bool
	Err         error
}

// Verifier recomputes the ledger hash chain sequentially and validates every
// checkpoint's Ed25519 signature (invariant I-07, D10).
type Verifier struct {
	store  *store.Store
	signer *store.Signer
}

// New constructs a Verifier; signer may be nil (checkpoint sigs then only
// structurally checked).
func New(st *store.Store, signer *store.Signer) *Verifier {
	return &Verifier{store: st, signer: signer}
}

// Verify walks the whole chain from genesis, failing closed on the first
// mismatch.
func (v *Verifier) Verify(ctx context.Context) (*Report, error) {
	rep := &Report{}
	rows, err := v.store.Pool.Query(ctx,
		`SELECT seq, id, type, version, payload_sha256, prev_hash, entry_hash FROM ctrl.ledger ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("verify read chain: %w", err)
	}
	defer rows.Close()

	var prevHash string
	expectPrev := ""
	for rows.Next() {
		var seq int64
		var id, typ string
		var version int
		var payloadSHA, gotPrev, gotEntry string
		if err := rows.Scan(&seq, &id, &typ, &version, &payloadSHA, &gotPrev, &gotEntry); err != nil {
			return nil, fmt.Errorf("verify scan: %w", err)
		}
		want := domain.ComputeEntryHash(seq, id, typ, version, payloadSHA, gotPrev)
		if want != gotEntry {
			rep.Err = fmt.Errorf("%w at seq=%d: entry_hash mismatch", domain.ErrChainBroken, seq)
			return rep, rep.Err
		}
		if expectPrev != "" && gotPrev != expectPrev {
			rep.Err = fmt.Errorf("%w at seq=%d: prev_hash does not chain to predecessor", domain.ErrChainBroken, seq)
			return rep, rep.Err
		}
		expectPrev = gotEntry
		prevHash = gotEntry
		rep.Entries++
		_ = prevHash
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("verify chain rows: %w", err)
	}

	cpRows, err := v.store.Pool.Query(ctx,
		`SELECT seq, entry_hash, sig FROM ctrl.ledger_checkpoints ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("verify read checkpoints: %w", err)
	}
	defer cpRows.Close()
	for cpRows.Next() {
		var seq int64
		var entryHash string
		var sig []byte
		if err := cpRows.Scan(&seq, &entryHash, &sig); err != nil {
			return nil, fmt.Errorf("verify checkpoint scan: %w", err)
		}
		if len(sig) != ed25519.SignatureSize {
			rep.Err = fmt.Errorf("%w at seq=%d: bad signature size", domain.ErrCheckpointInvalid, seq)
			return rep, rep.Err
		}
		if v.signer != nil && !v.signer.VerifyCheckpoint(seq, entryHash, sig) {
			rep.Err = fmt.Errorf("%w at seq=%d", domain.ErrCheckpointInvalid, seq)
			return rep, rep.Err
		}
		rep.Checkpoints++
	}
	if err := cpRows.Err(); err != nil {
		return nil, fmt.Errorf("verify checkpoint rows: %w", err)
	}
	rep.OK = true
	return rep, nil
}
