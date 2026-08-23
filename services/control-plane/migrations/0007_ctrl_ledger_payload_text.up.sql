-- I-07 served-envelope integrity: ctrl.ledger.payload was jsonb, which
-- renormalizes stored JSON (key reordering by length+bytes, number
-- formatting). Served envelopes then no longer matched their recorded
-- payload_sha256 (hash is computed over canonical bytes at append time).
-- TEXT preserves the exact canonical bytes hashed into the chain.
BEGIN;

ALTER TABLE ctrl.ledger
    ALTER COLUMN payload SET DATA TYPE text USING payload::text;

COMMIT;
