package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// Queryer is the common surface of pgxpool.Pool and pgx.Tx so helpers work
// both standalone and inside a transaction.
type Queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Store owns every ctrl-schema persistence access for this process.
type Store struct {
	Pool   *pgxpool.Pool
	signer *Signer
}

// Open connects the pool; the caller must Close. The session timezone is
// pinned to UTC so all timestamps serialize deterministically.
func Open(ctx context.Context, dsn string) (*Store, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store parse dsn: %w", err)
	}
	poolCfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("store open: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// UseSigningKey loads the Ed25519 private key used to sign ledger checkpoints
// every 10k events. Without a key checkpoints are skipped in dev.
func (s *Store) UseSigningKey(keyFile string) error {
	if keyFile == "" {
		return nil
	}
	sig, err := LoadSigningKey(keyFile)
	if err != nil {
		return err
	}
	s.signer = sig
	return nil
}

// Close releases pool resources.
func (s *Store) Close() { s.Pool.Close() }

// Migrate applies any not-yet-applied *.up.sql files under dir in name order,
// tracking versions in ctrl.schema_migrations.
func (s *Store) Migrate(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("migrate read dir %s: %w", dir, err)
	}
	type mig struct {
		version int
		file    string
	}
	var ups []mig
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		base := strings.TrimSuffix(name, ".up.sql")
		var version int
		if _, err := fmt.Sscanf(base, "%d", &version); err != nil {
			return fmt.Errorf("migrate bad migration name %s: %w", name, err)
		}
		ups = append(ups, mig{version, filepath.Join(dir, name)})
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].version < ups[j].version })

	for _, m := range ups {
		err := s.withTx(ctx, func(tx pgx.Tx) error {
			var done bool
			if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS ctrl`); err != nil {
				return err
			}
			err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='ctrl' AND table_name='schema_migrations')`,
			).Scan(&done)
			if err != nil {
				return err
			}
			if !done {
				if _, err := tx.Exec(ctx,
					`CREATE TABLE IF NOT EXISTS ctrl.schema_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
				); err != nil {
					return err
				}
			}
			var applied bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM ctrl.schema_migrations WHERE version=$1)`, m.version,
			).Scan(&applied); err != nil {
				return err
			}
			if applied {
				return nil
			}
			sqlBytes, err := os.ReadFile(m.file)
			if err != nil {
				return fmt.Errorf("migrate read %s: %w", m.file, err)
			}
			if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
				return fmt.Errorf("migrate apply %s: %w", m.file, err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO ctrl.schema_migrations (version) VALUES ($1)`, m.version)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) withTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// ExecTx runs fn atomically in a single transaction; an error rolls back.
func (s *Store) ExecTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return s.withTx(ctx, fn)
}

// appendLockKey serializes hash-chain appends across replicas.
const appendLockKey int64 = 0x53415552 // "SAUR"

// genesisHash seeds prev_hash for the first ledger entry.
var genesisHash = domain.HashPrefix + strings.Repeat("0", 64)

func jsonUnmarshal(raw []byte, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("unmarshal jsonb: %w", err)
	}
	return nil
}

// TailEvents reads ledger envelopes after afterSeq ordered by seq, optionally
// filtered by comma-separated event types and an "aggregateType:id" filter.
func (s *Store) TailEvents(ctx context.Context, tenantID string, afterSeq int64, types []string, aggregate string, limit int) ([]*domain.Event, int64, error) {
	where := []string{"tenant_id = $1", "seq > $2"}
	args := []any{tenantID, afterSeq}
	if len(types) > 0 {
		args = append(args, types)
		where = append(where, fmt.Sprintf("type = ANY($%d)", len(args)))
	}
	if aggregate != "" {
		parts := strings.SplitN(aggregate, ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			args = append(args, parts[0], parts[1])
			where = append(where, fmt.Sprintf("aggregate_type = $%d AND aggregate_id = $%d", len(args)-1, len(args)))
		}
	}
	args = append(args, limit)
	sqlStr := `SELECT id, seq, type, version, tenant_id, aggregate_type, aggregate_id,
 causation_id, correlation_id, actor, payload, payload_sha256, prev_hash, entry_hash, occurred_at
 FROM ctrl.ledger WHERE ` + strings.Join(where, " AND ") + ` ORDER BY seq LIMIT $` + fmt.Sprint(len(args))
	rows, err := s.Pool.Query(ctx, sqlStr, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("tail events: %w", err)
	}
	defer rows.Close()
	var events []*domain.Event
	var maxSeq int64
	for rows.Next() {
		ev := &domain.Event{}
		var aggType, aggID string
		var actor, payload []byte
		if err := rows.Scan(&ev.ID, &ev.Seq, &ev.Type, &ev.Version, &ev.TenantID, &aggType, &aggID,
			&ev.CausationID, &ev.CorrelationID, &actor, &payload, &ev.PayloadSHA256, &ev.PrevHash,
			&ev.EntryHash, &ev.OccurredAt); err != nil {
			return nil, 0, fmt.Errorf("scan event: %w", err)
		}
		ev.Aggregate = domain.AggregateRef{Type: aggType, ID: aggID}
		if err := jsonUnmarshal(actor, &ev.Actor); err != nil {
			return nil, 0, err
		}
		if err := jsonUnmarshal(payload, &ev.Payload); err != nil {
			return nil, 0, err
		}
		events = append(events, ev)
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("tail events rows: %w", err)
	}
	if maxSeq == 0 {
		if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(seq),0) FROM ctrl.ledger`).Scan(&maxSeq); err != nil {
			return nil, 0, fmt.Errorf("tail events head: %w", err)
		}
	}
	return events, maxSeq, nil
}

// MaxSeq returns the current chain head sequence.
func (s *Store) MaxSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(seq),0) FROM ctrl.ledger`).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("max seq: %w", err)
	}
	return seq, nil
}
