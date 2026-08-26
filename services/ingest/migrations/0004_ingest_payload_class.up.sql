-- ingest 0004 (T6 residual-risk closure): persisted replay seen-window keys.
-- payload_class_hash  = sha256(source|repo|event_kind|normalized-significant-
--                        payload-fields); content identity independent of the
--                        GitHub delivery GUID so near-replays under FRESH GUIDs
--                        are detectable during outage windows.
-- duplicate_suspect   = durable diagnosis flag; set when a fresh-GUID delivery
--                       matched a recent class hash. Status lifecycle stays
--                       normal (pending/duplicate_suspect -> forwarded), the
--                       boolean survives forwarding.
BEGIN;

ALTER TABLE ingest.deliveries ADD COLUMN IF NOT EXISTS payload_class_hash text NOT NULL DEFAULT '';
ALTER TABLE ingest.deliveries ADD COLUMN IF NOT EXISTS duplicate_suspect boolean NOT NULL DEFAULT false;

ALTER TABLE ingest.deliveries
    DROP CONSTRAINT IF EXISTS deliveries_status_check;
ALTER TABLE ingest.deliveries
    ADD CONSTRAINT deliveries_status_check
    CHECK (status IN ('pending', 'forwarded', 'forward_failed', 'rejected', 'duplicate_suspect'));

-- Secondary index for the seen-window backstop query (recent same-class
-- deliveries per source/repo) and forensic investigation.
CREATE INDEX IF NOT EXISTS deliveries_payload_class_idx
    ON ingest.deliveries (source, repo, payload_class_hash, received_at DESC);

COMMIT;
