package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UpsertInstallation implements Store: idempotent insert-or-refresh so
// installation.created redeliveries converge instead of erroring.
func (s *PGStore) UpsertInstallation(ctx context.Context, inst Installation) error {
	permJSON, err := json.Marshal(inst.Permissions)
	if err != nil {
		return fmt.Errorf("ghconn store: marshal permissions: %w", err)
	}
	if permJSON == nil {
		permJSON = []byte(`{}`)
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO ghconn.installations (id, account_login, suspended, permissions, updated_at)
		 VALUES ($1,$2,$3,$4,now())
		 ON CONFLICT (id) DO UPDATE SET
		   account_login=EXCLUDED.account_login,
		   suspended=EXCLUDED.suspended,
		   permissions=EXCLUDED.permissions,
		   updated_at=now()`,
		inst.ID, inst.AccountLogin, inst.Suspended, permJSON)
	if err != nil {
		return fmt.Errorf("ghconn store: upsert installation %d: %w", inst.ID, err)
	}
	return nil
}

// MarkSuspended implements Store.
func (s *PGStore) MarkSuspended(ctx context.Context, installationID int64, suspended bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE ghconn.installations SET suspended=$2, updated_at=now() WHERE id=$1`,
		installationID, suspended)
	if err != nil {
		return fmt.Errorf("ghconn store: mark suspended %d: %w", installationID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LinkRepo implements Store: idempotent edge upsert feeding fail-closed
// repo→installation resolution (plan §5.5.3).
func (s *PGStore) LinkRepo(ctx context.Context, installationID int64, owner, repo string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO ghconn.installation_repos (installation_id, owner, repo)
		 VALUES ($1,$2,$3)
		 ON CONFLICT (owner, repo) DO UPDATE SET installation_id=EXCLUDED.installation_id`,
		installationID, owner, repo)
	if err != nil {
		return fmt.Errorf("ghconn store: link repo %s/%s: %w", owner, repo, err)
	}
	return nil
}

// ResolveInstallation implements Store; unknown repos return ErrNotFound and
// callers MUST fail closed — guessing an installation risks cross-tenant
// check writes (§6.3).
func (s *PGStore) ResolveInstallation(ctx context.Context, owner, repo string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`SELECT installation_id FROM ghconn.installation_repos WHERE owner=$1 AND repo=$2`,
		owner, repo).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("ghconn store: resolve %s/%s: %w", owner, repo, err)
	}
	return id, nil
}

// RecordCheckReport implements Store. WHY transactional: retiring prior live
// rows and inserting the new decision must be atomic or a crash between the
// two steps leaves two live rows for one revision, tripping the partial
// unique index on every later write for that revision.
func (s *PGStore) RecordCheckReport(ctx context.Context, rep CheckReport) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE ghconn.check_reports SET live=false
			 WHERE candidate_id=$1 AND head_sha=$2 AND live AND decision_id<>$3`,
			rep.CandidateID, rep.HeadSHA, rep.DecisionID); err != nil {
			return fmt.Errorf("retire live reports: %w", err)
		}
		tag, err := tx.Exec(ctx,
			`INSERT INTO ghconn.check_reports
			   (decision_id, candidate_id, repo, head_sha, verb, conclusion, check_run_id, dry_run, live)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true)
			 ON CONFLICT (decision_id) DO NOTHING`,
			rep.DecisionID, rep.CandidateID, rep.Repo, rep.HeadSHA, string(rep.Verb),
			rep.Conclusion, rep.CheckRunID, rep.DryRun)
		if err != nil {
			return fmt.Errorf("insert report: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrDuplicate
		}
		return observeDeliveryTx(ctx, tx, rep.Repo)
	})
	if errors.Is(err, ErrDuplicate) {
		return ErrDuplicate
	}
	return err
}

// observeDeliveryTx bumps the per-repo delivery observation inside the same
// tx as the report persist. WHY here: the decisions handler is out of this
// builder's ownership boundary, yet GET /v1/installations/status needs
// per-repo delivery recency — a persisted report IS proof of a received
// delivery, so recording it at the storage layer keeps the signal honest.
func observeDeliveryTx(ctx context.Context, tx pgx.Tx, repo string) error {
	if repo == "" {
		return nil
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO ghconn.repo_delivery_observations (repo, last_seq, last_event_at)
		 VALUES ($1, 1, now())
		 ON CONFLICT (repo) DO UPDATE SET last_seq=ghconn.repo_delivery_observations.last_seq+1,
		   last_event_at=now()`, repo)
	if err != nil {
		return fmt.Errorf("observe delivery: %w", err)
	}
	return nil
}

// InstallationStatuses renders {installations:[...]} from ghconn tables only
// (no cross-schema reads). webhook_state: receiving if the repo's last
// observed delivery is newer than stalledAfter, else stalled; pending when
// nothing was ever observed.
func (s *PGStore) InstallationStatuses(ctx context.Context, stalledAfter time.Duration, now time.Time) ([]InstallationStatus, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT i.id, i.account_login, i.suspended, i.permissions,
		        r.repo, COALESCE(o.last_seq,0), o.last_event_at
		 FROM ghconn.installations i
		 LEFT JOIN ghconn.installation_repos r ON r.installation_id = i.id
		 LEFT JOIN ghconn.repo_delivery_observations o
		        ON o.repo = r.owner || '/' || r.repo
		 ORDER BY i.id, r.repo`)
	if err != nil {
		return nil, fmt.Errorf("ghconn store: status query: %w", err)
	}
	defer rows.Close()
	out := map[int64]*InstallationStatus{}
	order := []int64{}
	for rows.Next() {
		var id int64
		var account string
		var suspended bool
		var permRaw []byte
		var repoName *string
		var lastSeq int64
		var lastEvent *time.Time
		if err := rows.Scan(&id, &account, &suspended, &permRaw, &repoName, &lastSeq, &lastEvent); err != nil {
			return nil, fmt.Errorf("ghconn store: status scan: %w", err)
		}
		st, ok := out[id]
		if !ok {
			perms := map[string]string{}
			if len(permRaw) > 0 {
				if err := json.Unmarshal(permRaw, &perms); err != nil {
					return nil, fmt.Errorf("ghconn store: status permissions: %w", err)
				}
			}
			st = &InstallationStatus{InstallationID: id, Account: account, Suspended: suspended, Permissions: perms}
			out[id] = st
			order = append(order, id)
		}
		if repoName != nil {
			st.Repos = append(st.Repos, RepoStatus{
				Name:            *repoName,
				WebhookState:    webhookState(lastEvent, stalledAfter, now),
				LastDeliverySeq: lastSeq,
				LastEventAt:     lastEvent,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ghconn store: status rows: %w", err)
	}
	result := make([]InstallationStatus, 0, len(order))
	for _, id := range order {
		st := out[id]
		if st.Repos == nil {
			st.Repos = []RepoStatus{}
		}
		result = append(result, *st)
	}
	return result, nil
}

// webhookState derives receiving|stalled|pending per the status contract.
func webhookState(lastEvent *time.Time, stalledAfter time.Duration, now time.Time) string {
	if lastEvent == nil {
		return "pending"
	}
	if now.Sub(*lastEvent) < stalledAfter {
		return "receiving"
	}
	return "stalled"
}
