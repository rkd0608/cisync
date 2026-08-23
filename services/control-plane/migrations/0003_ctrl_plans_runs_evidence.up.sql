CREATE TABLE ctrl.validation_plans (
    tenant_id               text        NOT NULL,
    id                      text        NOT NULL,
    seq                     bigint      NOT NULL,
    candidate_id            text        NOT NULL,
    policy                  jsonb       NOT NULL,
    tiers                   jsonb       NOT NULL,
    required_evidence_kinds jsonb       NOT NULL,
    inputs_hash             text        NOT NULL,
    state                   text        NOT NULL,
    created_at              timestamptz NOT NULL,
    PRIMARY KEY (id)
);
CREATE INDEX idx_plans_candidate ON ctrl.validation_plans (candidate_id);

CREATE TABLE ctrl.validation_runs (
    tenant_id           text        NOT NULL,
    id                  text        NOT NULL,
    seq                 bigint      NOT NULL,
    plan_id             text        NOT NULL,
    candidate_id        text        NOT NULL,
    tier                integer     NOT NULL,
    job_spec            jsonb       NOT NULL,
    attempt             integer     NOT NULL DEFAULT 1,
    pool                text        NOT NULL,
    est_duration_ms     integer     NOT NULL DEFAULT 0,
    est_cost_millicents bigint      NOT NULL DEFAULT 0,
    priority            double precision NOT NULL DEFAULT 0,
    fence_token         bigint      NOT NULL DEFAULT 0,
    timeout_ms          integer     NOT NULL DEFAULT 900000,
    dispatched_at       timestamptz,
    finished_at         timestamptz,
    state               text        NOT NULL,
    created_at          timestamptz NOT NULL,
    PRIMARY KEY (id)
);
CREATE INDEX idx_runs_scheduler_scan ON ctrl.validation_runs (state, priority DESC) WHERE state = 'queued';
CREATE INDEX idx_runs_candidate ON ctrl.validation_runs (candidate_id);
CREATE INDEX idx_runs_stale_dispatch ON ctrl.validation_runs (dispatched_at) WHERE state IN ('queued', 'dispatched');

CREATE TABLE ctrl.evidence_records (
    tenant_id         text        NOT NULL,
    id                text        NOT NULL,
    seq               bigint      NOT NULL,
    run_id            text        NOT NULL,
    attempt           integer     NOT NULL,
    candidate_id      text        NOT NULL,
    kind              text        NOT NULL,
    verdict           text        NOT NULL,
    status            text        NOT NULL,
    digests           jsonb       NOT NULL DEFAULT '[]'::jsonb,
    inputs_hash       text        NOT NULL,
    confidence        double precision NOT NULL DEFAULT 0,
    cost_millicents   bigint      NOT NULL DEFAULT 0,
    produced_by_lease text,
    accepted_at       timestamptz,
    invalidated_reason text,
    created_at        timestamptz NOT NULL,
    PRIMARY KEY (id)
);
-- I-03: at most one accepted evidence record per (run_id, attempt).
CREATE UNIQUE INDEX idx_evidence_one_accepted_per_attempt
    ON ctrl.evidence_records (run_id, attempt) WHERE status = 'accepted';
