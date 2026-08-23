-- I-02 duplicate detection keys on the FULL submission identity: identical
-- head_sha under a different base_sha is a changed input (cache miss), not a
-- duplicate — it must plan fresh with its own inputs_hash.
-- Schema-qualified DROP: unqualified names resolve against search_path
-- (public), silently missing the ctrl-schema index.
BEGIN;

DROP INDEX IF EXISTS ctrl.idx_candidates_intent_head_live;

CREATE UNIQUE INDEX idx_candidates_intent_head_base_live
    ON ctrl.candidates (intent_id, head_sha, base_sha)
    WHERE state NOT IN ('superseded', 'cancelled');

COMMIT;
