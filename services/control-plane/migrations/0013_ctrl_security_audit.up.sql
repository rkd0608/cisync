-- ctrl 0013 (THREAT_MODEL B7): dedicated security-audit stream.
-- One row per security-grade event (authn/authz failures, signature
-- failures, budget violations, tamper detections, teardown revocations,
-- chain verification failures). Append-only by convention; retention is
-- enforced by the reconciler via CISYNC_CTRL_AUDIT_RETENTION_DAYS (>=90d,
-- default 90) — the table is NOT part of the hash-chained ledger because
-- audit pruning must be able to delete old rows.
BEGIN;

CREATE TABLE IF NOT EXISTS ctrl.security_audit (
    id         text        PRIMARY KEY,
    ts         timestamptz NOT NULL DEFAULT now(),
    tenant_id  text        NOT NULL,
    event_kind text        NOT NULL,
    actor      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    subject    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    detail     jsonb       NOT NULL DEFAULT '{}'::jsonb
);

-- Retention prune scans by age; kind+ts serves per-kind investigations.
CREATE INDEX IF NOT EXISTS idx_security_audit_ts ON ctrl.security_audit (ts);
CREATE INDEX IF NOT EXISTS idx_security_audit_kind_ts ON ctrl.security_audit (event_kind, ts);

COMMIT;
