-- P0-3 windowing: budgets.per_tenant_hour is an HOURLY ceiling, so usage
-- must decay per hour instead of accumulating as an all-time quota (the W4
-- storm exhausted a tenant permanently). The window stamp lets reads and
-- reserves lazily zero counters whose hour bucket has rolled over.
ALTER TABLE ctrl.budget_counters
    ADD COLUMN IF NOT EXISTS window_started_at timestamptz NOT NULL DEFAULT now();
