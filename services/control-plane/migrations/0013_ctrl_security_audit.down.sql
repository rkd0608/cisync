BEGIN;

DROP INDEX IF EXISTS ctrl.idx_security_audit_kind_ts;
DROP INDEX IF EXISTS ctrl.idx_security_audit_ts;
DROP TABLE IF EXISTS ctrl.security_audit;

COMMIT;
