BEGIN;

DROP INDEX IF EXISTS ghconn.uq_pending_writes_key_live;
DROP INDEX IF EXISTS ghconn.idx_pending_writes_due;
DROP TABLE IF EXISTS ghconn.pending_writes;

COMMIT;
