BEGIN;

DROP INDEX IF EXISTS ctrl.idx_candidates_intent_head_base_live;

CREATE UNIQUE INDEX idx_candidates_intent_head_live
    ON ctrl.candidates (intent_id, head_sha)
    WHERE state NOT IN ('superseded', 'cancelled');

COMMIT;
