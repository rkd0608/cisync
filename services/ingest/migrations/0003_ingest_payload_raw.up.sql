-- T6 audit anchor fidelity: deliveries.payload was jsonb, so malformed
-- (non-JSON) payloads — exactly the EC-005 quarantine case — failed the
-- INSERT and turned a valid-signature delivery into a 503. The audit anchor
-- must store the RAW received bytes verbatim; redaction happens before
-- forwarding, not before persistence.
BEGIN;

ALTER TABLE ingest.deliveries
    ALTER COLUMN payload SET DATA TYPE text USING payload::text;

COMMIT;
