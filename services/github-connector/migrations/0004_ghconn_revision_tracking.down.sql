-- ghconn 0004 down: drop the durable revision-tracking projection.

BEGIN;

DROP INDEX IF EXISTS ghconn.idx_revision_tracking_open;
DROP INDEX IF EXISTS ghconn.idx_revision_tracking_decision;
DROP TABLE IF EXISTS ghconn.revision_tracking;

COMMIT;
