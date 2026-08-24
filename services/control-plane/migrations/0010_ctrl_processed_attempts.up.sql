-- P1-4 poison-row handling: bounded retry bookkeeping for completion-feed
-- rows that fail transiently. attempts counts consecutive tick failures;
-- once the cap is reached the row is absorbed as a processed diagnostic so
-- one poison row can never wedge the feed. Absorbed rows live in
-- ctrl.processed_events (I-12 gate) — this table only tracks pre-absorption
-- failure counts.
ALTER TABLE ctrl.processed_events ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS ctrl.feed_retries (
    consumer   text        NOT NULL,
    event_key  text        NOT NULL,
    attempts   integer     NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_key)
);
