BEGIN;
DROP TABLE IF EXISTS ctrl.repo_suspensions;
DROP INDEX IF EXISTS ctrl.idx_intents_repo_pr;
DROP INDEX IF EXISTS ctrl.idx_candidates_repo_pr;
ALTER TABLE ctrl.candidates DROP COLUMN IF EXISTS rerun_count;
ALTER TABLE ctrl.candidates DROP COLUMN IF EXISTS pr_number;
ALTER TABLE ctrl.candidates DROP COLUMN IF EXISTS repo;
ALTER TABLE ctrl.intents DROP COLUMN IF EXISTS pr_number;
COMMIT;
