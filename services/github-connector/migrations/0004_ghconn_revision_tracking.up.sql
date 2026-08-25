-- ghconn 0004: durable revision tracking backing the connector's
-- tracking.Store seam (plan §4.1): ONE row per candidate revision walking
-- queued → in_progress → completed, plus the last applied decision payload
-- for replay_cached re-runs. Survives connector restarts, unlike the
-- in-memory default it replaces (integrator wiring W5).

BEGIN;

CREATE TABLE IF NOT EXISTS ghconn.revision_tracking (
    candidate_id  text        NOT NULL,
    head_sha      text        NOT NULL,
    repo          text        NOT NULL DEFAULT '',
    check_run_id  bigint      NOT NULL DEFAULT 0,
    phase         text        NOT NULL DEFAULT '',
    conclusion    text        NOT NULL DEFAULT '',
    decision_id   text        NOT NULL DEFAULT '',
    last_decision jsonb,
    stalled       boolean     NOT NULL DEFAULT false,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (candidate_id, head_sha)
);

-- decision-push idempotency lookups (FindByDecision); empty decision_id rows
-- (lifecycle-only revisions) never match.
CREATE INDEX IF NOT EXISTS idx_revision_tracking_decision
    ON ghconn.revision_tracking (decision_id)
    WHERE decision_id <> '';

-- Stalled-sweeper feed (OpenCheckReports): non-completed revisions by age.
CREATE INDEX IF NOT EXISTS idx_revision_tracking_open
    ON ghconn.revision_tracking (updated_at)
    WHERE phase <> 'completed';

COMMIT;
