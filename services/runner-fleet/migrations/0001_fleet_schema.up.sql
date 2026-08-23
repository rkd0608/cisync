BEGIN;

CREATE SCHEMA IF NOT EXISTS fleet;

CREATE TABLE IF NOT EXISTS fleet.workers (
    id             text PRIMARY KEY,
    pool           text NOT NULL,
    capacity       integer NOT NULL DEFAULT 1,
    last_heartbeat timestamptz NOT NULL DEFAULT now(),
    registered_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS fleet.execution_jobs (
    id             text PRIMARY KEY,
    run_id         text NOT NULL,
    attempt        integer NOT NULL DEFAULT 1,
    provider       text,
    pool           text NOT NULL,
    tier           integer NOT NULL DEFAULT 1,
    status         text NOT NULL DEFAULT 'queued',
    fence_token    bigint NOT NULL DEFAULT 0,
    claimed_by     text REFERENCES fleet.workers(id) ON DELETE SET NULL,
    claimed_at     timestamptz,
    last_heartbeat timestamptz,
    finished_at    timestamptz,
    result_ref     jsonb,
    accepted       boolean NOT NULL DEFAULT false,
    job_spec       jsonb NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT execution_jobs_run_id_unique UNIQUE (run_id),
    CONSTRAINT execution_jobs_status_check CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'timed_out', 'cancelled'))
);

CREATE INDEX IF NOT EXISTS execution_jobs_claim_idx
    ON fleet.execution_jobs (pool, created_at, id)
    WHERE status = 'queued';

CREATE INDEX IF NOT EXISTS execution_jobs_stale_idx
    ON fleet.execution_jobs (last_heartbeat)
    WHERE status = 'running';

CREATE TABLE IF NOT EXISTS fleet.artifacts (
    digest          text PRIMARY KEY,
    kind            text NOT NULL DEFAULT '',
    size_bytes      bigint NOT NULL DEFAULT 0,
    content_type    text NOT NULL DEFAULT 'application/octet-stream',
    payload         bytea,
    storage_key     text,
    produced_by_job text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT artifacts_digest_format CHECK (digest ~ '^sha256:[0-9a-f]{64}$')
);

CREATE INDEX IF NOT EXISTS artifacts_producer_idx
    ON fleet.artifacts (kind, produced_by_job);

COMMIT;
