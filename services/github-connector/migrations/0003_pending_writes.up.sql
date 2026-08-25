-- ghconn 0003: outbox-style pending writes for the connector-local write
-- budget (plan §4.6): budget exhaustion or GitHub unavailability QUEUES the
-- required-check write here instead of dropping it silently.

BEGIN;

CREATE TABLE IF NOT EXISTS ghconn.pending_writes (
    id              text PRIMARY KEY,
    key             text NOT NULL,
    installation_id bigint NOT NULL,
    repo            text NOT NULL,
    op              text NOT NULL CHECK (op IN ('create_check','update_check')),
    check_run_id    bigint NOT NULL DEFAULT 0,
    payload         jsonb NOT NULL,
    attempts        integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz
);

-- WHY partial unique on key WHERE undelivered: redelivered enqueues of the
-- same §4 idempotency basis collapse while delivered rows roll off freely.
CREATE UNIQUE INDEX IF NOT EXISTS uq_pending_writes_key_live
    ON ghconn.pending_writes (key)
    WHERE delivered_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_pending_writes_due
    ON ghconn.pending_writes (next_attempt_at)
    WHERE delivered_at IS NULL;

COMMIT;
