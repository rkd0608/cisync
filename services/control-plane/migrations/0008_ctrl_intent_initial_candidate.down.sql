BEGIN;

ALTER TABLE ctrl.intents DROP COLUMN IF EXISTS initial_candidate_id;

COMMIT;
