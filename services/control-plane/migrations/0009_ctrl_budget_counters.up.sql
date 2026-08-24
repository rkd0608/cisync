-- Budget conservation ledger (I-06): per-tenant counters that reservation
-- (admission dispatch tx) increments and release (terminal-state tx)
-- decrements, replacing the W3 sentinel re-seeding. Σreservations −
-- Σreleases == used holds because every counter mutation commits in the
-- SAME transaction as the state flip it underwrites.
CREATE TABLE IF NOT EXISTS ctrl.budget_counters (
    tenant_id   text        NOT NULL,
    kind        text        NOT NULL,
    used        bigint      NOT NULL DEFAULT 0,
    updated_seq bigint      NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, kind),
    CONSTRAINT budget_counters_kind_check CHECK (kind IN (
        'cpu_minutes', 'environment_minutes', 'repair_attempts', 'concurrent_candidates'
    ))
);
