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
