BEGIN;

DROP INDEX IF EXISTS ingest.deliveries_source_ext_unique_sigok;

ALTER TABLE ingest.deliveries
    DROP CONSTRAINT IF EXISTS deliveries_status_check;

ALTER TABLE ingest.deliveries
    ADD CONSTRAINT deliveries_status_check
    CHECK (status IN ('pending', 'forwarded', 'forward_failed'));

ALTER TABLE ingest.deliveries
    ADD CONSTRAINT deliveries_source_ext_unique UNIQUE (source, ext_delivery_id);

COMMIT;
