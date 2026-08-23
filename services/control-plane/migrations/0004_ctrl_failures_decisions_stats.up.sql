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
