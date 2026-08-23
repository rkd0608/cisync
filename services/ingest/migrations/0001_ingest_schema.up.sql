BEGIN;

CREATE SCHEMA IF NOT EXISTS ingest;

CREATE TABLE IF NOT EXISTS ingest.deliveries (
    id              text PRIMARY KEY,
    source          text NOT NULL,
    ext_delivery_id text NOT NULL,
    event_kind      text NOT NULL DEFAULT '',
    repo            text NOT NULL DEFAULT '',
    sig_ok          boolean NOT NULL DEFAULT false,
    headers         jsonb NOT NULL DEFAULT '{}'::jsonb,
    payload         jsonb NOT NULL,
    status          text NOT NULL DEFAULT 'pending',
    attempts        integer NOT NULL DEFAULT 0,
    received_at     timestamptz NOT NULL DEFAULT now(),
    last_attempt_at timestamptz,
    forwarded_at    timestamptz,
    CONSTRAINT deliveries_source_ext_unique UNIQUE (source, ext_delivery_id),
    CONSTRAINT deliveries_status_check CHECK (status IN ('pending', 'forwarded', 'forward_failed'))
);

CREATE INDEX IF NOT EXISTS deliveries_retry_idx
    ON ingest.deliveries (status, COALESCE(last_attempt_at, received_at))
    WHERE status <> 'forwarded';

CREATE INDEX IF NOT EXISTS deliveries_received_brin
    ON ingest.deliveries USING brin (received_at);

COMMIT;
