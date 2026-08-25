-- ghconn 0002 (plan §5.6): installation lifecycle columns, repo→installation
-- cache, one-live-check-run-per-candidate-revision, delivery observations.

BEGIN;

ALTER TABLE ghconn.installations ADD COLUMN IF NOT EXISTS suspended boolean NOT NULL DEFAULT false;
ALTER TABLE ghconn.installations ADD COLUMN IF NOT EXISTS permissions jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE ghconn.installations ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS ghconn.installation_repos (
    installation_id bigint NOT NULL REFERENCES ghconn.installations(id) ON DELETE CASCADE,
    owner      text        NOT NULL,
    repo       text        NOT NULL,
    linked_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner, repo)
);

-- Plan §4.1: ONE live check run per candidate revision (queued → in_progress →
-- completed walks the SAME run). History rows survive with live=false.
ALTER TABLE ghconn.check_reports ADD COLUMN IF NOT EXISTS live boolean NOT NULL DEFAULT true;
CREATE UNIQUE INDEX IF NOT EXISTS uq_check_reports_live_revision
    ON ghconn.check_reports (candidate_id, head_sha) WHERE live;

-- Per-repo delivery observations ground the status endpoint's webhook_state
-- (receiving <15m / stalled / pending); fed inside the report persist path.
CREATE TABLE IF NOT EXISTS ghconn.repo_delivery_observations (
    repo          text        PRIMARY KEY,
    last_seq      bigint      NOT NULL DEFAULT 0,
    last_event_at timestamptz NOT NULL DEFAULT now()
);

COMMIT;
