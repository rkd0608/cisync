-- ghconn schema: github-connector owns installations + check_reports per
-- ARCHITECTURE §2.

BEGIN;

CREATE SCHEMA IF NOT EXISTS ghconn;

CREATE TABLE IF NOT EXISTS ghconn.installations (
    id            bigint PRIMARY KEY,
    account_login text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ghconn.check_reports (
    decision_id  text PRIMARY KEY,
    candidate_id text NOT NULL,
    repo         text NOT NULL,
    head_sha     text NOT NULL,
    verb         text NOT NULL,
    conclusion   text NOT NULL,
    check_run_id bigint NOT NULL DEFAULT 0,
    dry_run      boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_check_reports_candidate
    ON ghconn.check_reports (candidate_id);

COMMIT;
