-- §3b lifecycle trace: the intent declaration reserves the FIRST candidate
-- slot so intent.declared / lease.granted carry the candidate correlation and
-- the documented event sequence is observable end-to-end. The slot is
-- consumed (cleared) atomically by the first submission.
BEGIN;

ALTER TABLE ctrl.intents ADD COLUMN IF NOT EXISTS initial_candidate_id text NOT NULL DEFAULT '';

COMMIT;
