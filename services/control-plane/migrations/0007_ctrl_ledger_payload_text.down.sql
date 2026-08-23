BEGIN;

ALTER TABLE ctrl.ledger
    ALTER COLUMN payload SET DATA TYPE jsonb USING payload::jsonb;

COMMIT;
