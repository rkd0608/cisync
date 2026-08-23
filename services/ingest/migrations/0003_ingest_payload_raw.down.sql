BEGIN;

ALTER TABLE ingest.deliveries
    ALTER COLUMN payload SET DATA TYPE jsonb USING payload::jsonb;

COMMIT;
