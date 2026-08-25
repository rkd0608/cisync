-- ctrl 0012 (plan §5.6): webhook-normalizer projection support.
-- (tenant_id, repo, pr_number) lookup for synchronize/reopen resolution,
-- rerun_count for the revalidate budget cap, repo suspensions for G10.

BEGIN;

ALTER TABLE ctrl.intents ADD COLUMN IF NOT EXISTS pr_number integer NOT NULL DEFAULT 0;

ALTER TABLE ctrl.candidates ADD COLUMN IF NOT EXISTS repo text NOT NULL DEFAULT '';
ALTER TABLE ctrl.candidates ADD COLUMN IF NOT EXISTS pr_number integer NOT NULL DEFAULT 0;
ALTER TABLE ctrl.candidates ADD COLUMN IF NOT EXISTS rerun_count integer NOT NULL DEFAULT 0;

-- Partial: only PR-bound rows need the index; full scans stay untouched.
CREATE INDEX IF NOT EXISTS idx_candidates_repo_pr
    ON ctrl.candidates (tenant_id, repo, pr_number) WHERE pr_number > 0;
CREATE INDEX IF NOT EXISTS idx_intents_repo_pr
    ON ctrl.intents (tenant_id, repo, pr_number) WHERE pr_number > 0;

-- G10 suspension flag for synthetic-intent creation; ledger remains source
-- of truth (projection is rebuildable by replaying installation.deleted).
CREATE TABLE IF NOT EXISTS ctrl.repo_suspensions (
    repo         text        PRIMARY KEY,
    suspended_at timestamptz NOT NULL DEFAULT now()
);

COMMIT;
