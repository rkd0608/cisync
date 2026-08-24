-- Job-lease credential storage (THREAT_MODEL B2 / I-04): control-plane mints
-- an Ed25519 job-lease JWT per dispatched run and pushes it in the enqueue
-- payload; the fleet persists it with the job, hands it to the claiming
-- worker, and requires it (Authorization: Bearer) on mutating endpoints.
ALTER TABLE fleet.execution_jobs ADD COLUMN IF NOT EXISTS lease_token text NOT NULL DEFAULT '';
