-- control-plane schema: ctrl owns ledger + projections per ARCHITECTURE §2.
-- NOTE: ctrl.schema_migrations is owned by the store migrator, not these files.

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
