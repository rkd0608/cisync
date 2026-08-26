package store

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// CheckpointInterval is the event count between signed chain checkpoints.
const CheckpointInterval = 10000

// Signer holds the Ed25519 checkpoint-signing identity.
type Signer struct {
	priv ed25519.PrivateKey
}

// LoadSigningKey parses an openssl-generated PEM private key file; only
// Ed25519 keys are accepted (D10).
func LoadSigningKey(path string) (*Signer, error) {
	pemBytes, err := osReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load ledger key: %w", err)
	}
	key, err := parseEd25519PEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("load ledger key %s: %w", path, err)
	}
	return &Signer{priv: key}, nil
}

// Public returns the hex-encoded public half for verification.
func (sg *Signer) Public() string {
	pub := sg.priv.Public().(ed25519.PublicKey)
	return hex.EncodeToString(pub)
}

// Sign returns an Ed25519 signature over the message.
func (sg *Signer) Sign(msg []byte) []byte {
	h := sha256.Sum256(msg)
	return ed25519.Sign(sg.priv, h[:])
}

// AppendEvents appends events to the hash-chained ledger and enqueues outbox
// rows in ONE transaction (I-07/I-12 spine). Seqs, prev_hash and entry_hash
// are stamped onto the passed events.
func (s *Store) AppendEvents(ctx context.Context, events []*domain.Event) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		return s.AppendEventsTx(ctx, tx, events)
	})
}

// AppendEventsTx is AppendEvents within a caller-supplied transaction so
// projections commit atomically with their events.
//
// WHY batching: the advisory lock serializes ALL ledger writers globally, so
// the critical section must be minimal — canonical JSON is computed BEFORE
// the lock and rows are written with two multi-value INSERTs (one per table)
// instead of 2N round trips. Callers must append every event of a tx in ONE
// call; a second AppendEventsTx in the same tx doubles global lock hold time
// (W3 storm finding: throughput collapse under 500-writer bursts).
func (s *Store) AppendEventsTx(ctx context.Context, tx pgx.Tx, events []*domain.Event) error {
	if len(events) == 0 {
		return nil
	}

	actorJSONs := make([][]byte, len(events))
	payloadJSONs := make([][]byte, len(events))
	for i, ev := range events {
		actorJSON, err := domain.CanonicalJSON(ev.Actor)
		if err != nil {
			return fmt.Errorf("canonical actor seq-prep %s: %w", ev.ID, err)
		}
		payloadJSON, err := domain.CanonicalJSON(ev.Payload)
		if err != nil {
			return fmt.Errorf("canonical payload prep %s: %w", ev.ID, err)
		}
		actorJSONs[i] = actorJSON
		payloadJSONs[i] = payloadJSON
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, appendLockKey); err != nil {
		return fmt.Errorf("append lock: %w", err)
	}
	var seq int64
	var prevHash string
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq),0), COALESCE((SELECT entry_hash FROM ctrl.ledger ORDER BY seq DESC LIMIT 1), $1) FROM ctrl.ledger`,
		genesisHash,
	).Scan(&seq, &prevHash)
	if err != nil {
		return fmt.Errorf("append tail read: %w", err)
	}
	startSeq := seq

	ledgerValues := make([]string, 0, len(events))
	outboxValues := make([]string, 0, len(events))
	seqArgs := make([]any, 0, len(events)*15)
	outboxArgs := make([]any, 0, len(events)*6)
	for i, ev := range events {
		seq++
		ev.Seq = seq
		ev.PrevHash = prevHash
		ev.EntryHash = domain.ComputeEntryHash(seq, ev.ID, ev.Type, ev.Version, ev.PayloadSHA256, prevHash)
		prevHash = ev.EntryHash

		base := len(seqArgs)
		seqArgs = append(seqArgs,
			ev.Seq, ev.ID, ev.Type, ev.Version, ev.TenantID, ev.Aggregate.Type, ev.Aggregate.ID,
			ev.CausationID, ev.CorrelationID, actorJSONs[i], payloadJSONs[i], ev.PayloadSHA256,
			ev.PrevHash, ev.EntryHash, ev.OccurredAt,
		)
		ledgerValues = append(ledgerValues, "("+placeholderGroup(base+1, 15)+")")

		obase := len(outboxArgs)
		outboxArgs = append(outboxArgs, ev.ID, ev.Seq, ev.TenantID, ev.Type, ev.Aggregate.Type, ev.Aggregate.ID)
		outboxValues = append(outboxValues, "("+placeholderGroup(obase+1, 6)+")")
	}

	ledgerQuery := `INSERT INTO ctrl.ledger (seq, id, type, version, tenant_id, aggregate_type, aggregate_id,
	   causation_id, correlation_id, actor, payload, payload_sha256, prev_hash, entry_hash, occurred_at)
	 VALUES ` + strings.Join(ledgerValues, ",")
	if _, err := tx.Exec(ctx, ledgerQuery, seqArgs...); err != nil {
		return fmt.Errorf("append ledger rows %d..%d: %w", startSeq+1, seq, err)
	}

	outboxQuery := `INSERT INTO ctrl.outbox (event_id, seq, tenant_id, type, aggregate_type, aggregate_id)
	 VALUES ` + strings.Join(outboxValues, ",")
	if _, err := tx.Exec(ctx, outboxQuery, outboxArgs...); err != nil {
		return fmt.Errorf("append outbox rows %d..%d: %w", startSeq+1, seq, err)
	}

	for boundary := (startSeq/CheckpointInterval + 1) * CheckpointInterval; boundary <= seq && s.signer != nil; boundary += CheckpointInterval {
		var entryHash string
		if err := tx.QueryRow(ctx, `SELECT entry_hash FROM ctrl.ledger WHERE seq=$1`, boundary).Scan(&entryHash); err != nil {
			return fmt.Errorf("checkpoint read seq=%d: %w", boundary, err)
		}
		sig := s.signer.SignCheckpoint(boundary, entryHash)
		if _, err := tx.Exec(ctx,
			`INSERT INTO ctrl.ledger_checkpoints (seq, entry_hash, sig) VALUES ($1,$2,$3)`,
			boundary, entryHash, sig,
		); err != nil {
			return fmt.Errorf("checkpoint insert seq=%d: %w", boundary, err)
		}
	}
	return nil
}

// placeholderGroup renders "($n,$n+1,...)" for multi-value INSERTs.
func placeholderGroup(start, count int) string {
	parts := make([]string, 0, count)
	for i := start; i < start+count; i++ {
		parts = append(parts, "$"+strconv.Itoa(i))
	}
	return strings.Join(parts, ",")
}
