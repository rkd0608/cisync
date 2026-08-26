BEGIN;

DROP INDEX IF EXISTS ingest.deliveries_payload_class_idx;

ALTER TABLE ingest.deliveries
    DROP CONSTRAINT IF EXISTS deliveries_status_check;
ALTER TABLE ingest.deliveries
    ADD CONSTRAINT deliveries_status_check
    CHECK (status IN ('pending', 'forwarded', 'forward_failed', 'rejected'));

ALTER TABLE ingest.deliveries
    DROP COLUMN IF EXISTS duplicate_suspect;
ALTER TABLE ingest.deliveries
    DROP COLUMN IF EXISTS payload_class_hash;

COMMIT;
