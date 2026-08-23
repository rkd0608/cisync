package store

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
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
func (s *Store) AppendEventsTx(ctx context.Context, tx pgx.Tx, events []*domain.Event) error {
	if len(events) == 0 {
		return nil
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
	for _, ev := range events {
		seq++
		ev.Seq = seq
		ev.PrevHash = prevHash
		ev.EntryHash = domain.ComputeEntryHash(seq, ev.ID, ev.Type, ev.Version, ev.PayloadSHA256, prevHash)
		actorJSON, err := domain.CanonicalJSON(ev.Actor)
		if err != nil {
			return err
		}
		payloadJSON, err := domain.CanonicalJSON(ev.Payload)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ctrl.ledger (seq, id, type, version, tenant_id, aggregate_type, aggregate_id,
			   causation_id, correlation_id, actor, payload, payload_sha256, prev_hash, entry_hash, occurred_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			ev.Seq, ev.ID, ev.Type, ev.Version, ev.TenantID, ev.Aggregate.Type, ev.Aggregate.ID,
			ev.CausationID, ev.CorrelationID, actorJSON, payloadJSON, ev.PayloadSHA256,
			ev.PrevHash, ev.EntryHash, ev.OccurredAt,
		); err != nil {
			return fmt.Errorf("append ledger row seq=%d: %w", seq, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ctrl.outbox (event_id, seq, tenant_id, type, aggregate_type, aggregate_id)
			 VALUES ($1,$2,$3,$4,$5,$6)`,
			ev.ID, ev.Seq, ev.TenantID, ev.Type, ev.Aggregate.Type, ev.Aggregate.ID,
		); err != nil {
			return fmt.Errorf("append outbox row seq=%d: %w", seq, err)
		}
		prevHash = ev.EntryHash
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
