-- control-plane schema: ctrl owns ledger + projections per ARCHITECTURE §2.
-- NOTE: ctrl.schema_migrations is owned by the store migrator, not this file.

CREATE SCHEMA IF NOT EXISTS ctrl;

CREATE TABLE ctrl.ledger (
    seq            bigint     NOT NULL,
    id             text       NOT NULL,
    type           text       NOT NULL,
    version        integer    NOT NULL DEFAULT 1,
    tenant_id      text       NOT NULL,
    aggregate_type text       NOT NULL,
    aggregate_id   text       NOT NULL,
    causation_id   text       NOT NULL DEFAULT '',
    correlation_id text       NOT NULL DEFAULT '',
    actor          jsonb      NOT NULL,
    payload        jsonb      NOT NULL,
    payload_sha256 text       NOT NULL,
    prev_hash      text       NOT NULL,
    entry_hash     text       NOT NULL,
    occurred_at    timestamptz NOT NULL,
    PRIMARY KEY (seq)
);
CREATE UNIQUE INDEX idx_ledger_id ON ctrl.ledger (id);
CREATE INDEX idx_ledger_aggregate ON ctrl.ledger (aggregate_type, aggregate_id, seq);
CREATE INDEX idx_ledger_correlation ON ctrl.ledger (correlation_id);

-- I-07/B1: ledger rows are INSERT/SELECT-only; corrections append compensating events.
CREATE FUNCTION ctrl.reject_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ctrl.ledger is append-only: % blocked on seq %', TG_OP, OLD.seq
        USING ERRCODE = 'raise_exception';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ledger_append_only
    BEFORE UPDATE OR DELETE ON ctrl.ledger
    FOR EACH ROW EXECUTE FUNCTION ctrl.reject_mutation();

CREATE TABLE ctrl.ledger_checkpoints (
    seq         bigint      NOT NULL,
    entry_hash  text        NOT NULL,
    sig         bytea       NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (seq)
);

CREATE TABLE ctrl.outbox (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id        text        NOT NULL,
    seq             bigint      NOT NULL,
    tenant_id       text        NOT NULL,
    type            text        NOT NULL,
    aggregate_type  text        NOT NULL,
    aggregate_id    text        NOT NULL,
    status          text        NOT NULL DEFAULT 'pending',
    attempts        integer     NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    published_at    timestamptz
);
CREATE UNIQUE INDEX idx_outbox_event_id ON ctrl.outbox (event_id);
CREATE INDEX idx_outbox_poll ON ctrl.outbox (status, next_attempt_at, id);

CREATE OR REPLACE FUNCTION ctrl.notify_outbox_changed() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('outbox_changed', '');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_outbox_notify
    AFTER INSERT ON ctrl.outbox
    FOR EACH ROW EXECUTE FUNCTION ctrl.notify_outbox_changed();

-- I-12: agent command idempotency; replays return the cached response body.
CREATE TABLE ctrl.command_log (
    tenant_id      text        NOT NULL,
    endpoint       text        NOT NULL,
    command_key    text        NOT NULL,
    request_hash   text        NOT NULL,
    response_code  integer     NOT NULL,
    response_body  jsonb       NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, endpoint, command_key)
);

-- I-12: consumer dedupe inside effect txs.
CREATE TABLE ctrl.processed_events (
    event_id     text        NOT NULL,
    consumer     text        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, consumer)
);

-- Projections (rebuildable from ledger replay; upsert keyed (aggregate_id, seq)).

CREATE TABLE ctrl.intents (
    tenant_id           text        NOT NULL,
    id                  text        NOT NULL,
    seq                 bigint      NOT NULL,
    state               text        NOT NULL,
    goal                text        NOT NULL,
    repo                text        NOT NULL,
    base_ref            text        NOT NULL,
    base_snapshot       text        NOT NULL,
    owned_surfaces      text[]      NOT NULL,
    constraints         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    acceptance_criteria jsonb       NOT NULL DEFAULT '[]'::jsonb,
    risk_class          text        NOT NULL,
    deadline            timestamptz,
    origin              text        NOT NULL,
    agent_lineage       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    resolved_policy     jsonb       NOT NULL,
    compute_budget      jsonb       NOT NULL,
    created_at          timestamptz NOT NULL,
    closed_at           timestamptz,
    PRIMARY KEY (id)
);
CREATE INDEX idx_intents_state_active ON ctrl.intents (tenant_id, state);
CREATE INDEX idx_intents_surfaces_gin ON ctrl.intents USING gin (owned_surfaces);
CREATE INDEX idx_intents_repo_active ON ctrl.intents (repo, state);

CREATE TABLE ctrl.leases (
    tenant_id      text        NOT NULL,
    id             text        NOT NULL,
    seq            bigint      NOT NULL,
    intent_id      text        NOT NULL,
    scope_kind     text        NOT NULL,
    surfaces       text[]      NOT NULL DEFAULT '{}',
    env_template   text,
    holder         text        NOT NULL,
    budget         jsonb       NOT NULL,
    ttl_expires_at timestamptz NOT NULL,
    renewal_count  integer     NOT NULL DEFAULT 0,
    queue_position integer,
    eta_seconds    integer,
    required_evidence jsonb    NOT NULL DEFAULT '[]'::jsonb,
    state          text        NOT NULL,
    created_at     timestamptz NOT NULL,
    released_at    timestamptz,
    PRIMARY KEY (id)
);
CREATE INDEX idx_leases_intent ON ctrl.leases (intent_id);
CREATE INDEX idx_leases_ttl ON ctrl.leases (state, ttl_expires_at);

CREATE TABLE ctrl.candidates (
    tenant_id             text        NOT NULL,
    id                    text        NOT NULL,
    seq                   bigint      NOT NULL,
    intent_id             text        NOT NULL,
    submitter             text        NOT NULL,
    patch_ref             text        NOT NULL,
    head_sha              text        NOT NULL,
    base_sha              text        NOT NULL,
    changed_paths         text[]      NOT NULL DEFAULT '{}',
    est_cost_millicents   bigint      NOT NULL DEFAULT 0,
    priority_score        double precision NOT NULL DEFAULT 0,
    cluster_id            text,
    relation_to_rep       text,
    state                 text        NOT NULL,
    created_at            timestamptz NOT NULL,
    PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_candidates_intent_head_live
    ON ctrl.candidates (intent_id, head_sha) WHERE state NOT IN ('superseded', 'cancelled');
CREATE INDEX idx_candidates_intent ON ctrl.candidates (intent_id, state);
CREATE INDEX idx_candidates_cluster ON ctrl.candidates (cluster_id);

CREATE TABLE ctrl.clusters (
    tenant_id        text        NOT NULL,
    id               text        NOT NULL,
    seq              bigint      NOT NULL,
    repo             text        NOT NULL,
    rep_candidate_id text        NOT NULL,
    member_count     integer     NOT NULL DEFAULT 1,
    state            text        NOT NULL,
    strategy_version text        NOT NULL,
    created_at       timestamptz NOT NULL,
    PRIMARY KEY (id)
);

CREATE TABLE ctrl.cluster_members (
    cluster_id       text   NOT NULL,
    candidate_id     text   NOT NULL,
    relation_to_rep  text   NOT NULL,
    similarity_score double precision NOT NULL DEFAULT 1.0,
    PRIMARY KEY (cluster_id, candidate_id)
);
CREATE INDEX idx_cluster_members_candidate ON ctrl.cluster_members (candidate_id);

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

CREATE TABLE ctrl.failure_cases (
    tenant_id                text        NOT NULL,
    id                       text        NOT NULL,
    seq                      bigint      NOT NULL,
    candidate_id             text        NOT NULL,
    run_id                   text        NOT NULL,
    signature_digest         text        NOT NULL,
    classification           text,
    classification_confidence double precision,
    reproduction_command     text        NOT NULL DEFAULT '',
    suspected_paths          text[]      NOT NULL DEFAULT '{}',
    routed_action            text        NOT NULL DEFAULT 'none',
    state                    text        NOT NULL,
    created_at               timestamptz NOT NULL,
    PRIMARY KEY (id)
);

CREATE TABLE ctrl.repair_tasks (
    tenant_id        text        NOT NULL,
    id               text        NOT NULL,
    seq              bigint      NOT NULL,
    failure_case_id  text        NOT NULL,
    candidate_id     text        NOT NULL,
    envelope         jsonb       NOT NULL,
    attempts_used    integer     NOT NULL DEFAULT 0,
    resulting_patch_refs jsonb   NOT NULL DEFAULT '[]'::jsonb,
    state            text        NOT NULL,
    created_at       timestamptz NOT NULL,
    PRIMARY KEY (id)
);

CREATE TABLE ctrl.policies (
    tenant_id    text   NOT NULL,
    id           text   NOT NULL,
    version      integer NOT NULL,
    status       text   NOT NULL,
    body         jsonb  NOT NULL,
    activated_by text,
    activated_at timestamptz,
    seq          bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (id, version)
);

CREATE TABLE ctrl.decisions (
    tenant_id     text        NOT NULL,
    id            text        NOT NULL,
    seq           bigint      NOT NULL,
    subject_type  text        NOT NULL,
    subject_id    text        NOT NULL,
    verb          text        NOT NULL,
    confidence    double precision NOT NULL,
    policy        jsonb       NOT NULL,
    explanation   jsonb       NOT NULL,
    evidence_refs jsonb       NOT NULL DEFAULT '[]'::jsonb,
    inputs_hash   text        NOT NULL,
    rendered_at   timestamptz NOT NULL,
    PRIMARY KEY (id)
);

CREATE TABLE ctrl.rate_limits (
    tenant_id   text        NOT NULL,
    bucket      text        NOT NULL,
    tokens      double precision NOT NULL,
    last_refill timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, bucket)
);

CREATE TABLE ctrl.stats_test_outcomes (
    job_kind      text NOT NULL,
    surface_class text NOT NULL,
    risk_class    text NOT NULL,
    observations  bigint NOT NULL DEFAULT 0,
    flips         bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (job_kind, surface_class, risk_class)
);
