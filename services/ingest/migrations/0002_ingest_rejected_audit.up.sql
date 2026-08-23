-- Reconcile dedup uniqueness with signature-rejection auditing (EC-003):
-- rejected (sig_ok=false) rows are quarantined audit records and must NOT
-- occupy the (source, ext_delivery_id) dedup slot, so a later valid
-- redelivery of the same GUID is still admitted. Only sig_ok rows collide.
BEGIN;

ALTER TABLE ingest.deliveries
    DROP CONSTRAINT IF EXISTS deliveries_source_ext_unique;

ALTER TABLE ingest.deliveries
    DROP CONSTRAINT IF EXISTS deliveries_status_check;

ALTER TABLE ingest.deliveries
    ADD CONSTRAINT deliveries_status_check
    CHECK (status IN ('pending', 'forwarded', 'forward_failed', 'rejected'));

CREATE UNIQUE INDEX IF NOT EXISTS deliveries_source_ext_unique_sigok
    ON ingest.deliveries (source, ext_delivery_id)
    WHERE sig_ok;

COMMIT;
