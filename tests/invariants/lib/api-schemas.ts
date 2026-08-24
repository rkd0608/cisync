import { z } from 'zod';

/**
 * Zod mirrors of packages/contracts/openapi.yaml + internal-protocols.md §2.
 * WHY: hand-kept but contract-derived; drift is caught because every live
 * probe parses through these before asserting. Kept in one file so the
 * mapping to the yaml is reviewable line-by-line.
 */

export const ERROR_CODES = [
  'validation_failed',
  'unauthorized',
  'forbidden',
  'not_found',
  'conflict_state',
  'rate_limited',
  'budget_exceeded',
  'unavailable',
] as const;

export const errorEnvelopeSchema = z.object({
  error: z.object({
    code: z.enum(ERROR_CODES),
    message: z.string(),
    details: z.record(z.unknown()).optional(),
    retry_after_s: z.number().int().nullable().optional(),
    suggestions: z.array(z.string()).optional(),
  }),
});
export type ErrorEnvelope = z.infer<typeof errorEnvelopeSchema>;

export const conflictReasonSchema = z
  .enum(['late_submission', 'intent_closed', 'expired_lease', 'revoked_lease', 'duplicate_sha', 'version_conflict', 'not_classified']);

export const intentGrantSchema = z.object({
  intent_id: z.string().regex(/^int_[0-9A-HJKMNP-TV-Z]{26}$/),
  lease_id: z.string().regex(/^lease_[0-9A-HJKMNP-TV-Z]{26}$/),
  base_snapshot: z.string(),
  worktree_or_branch: z.string(),
  allowed_paths: z.array(z.string()),
  prohibited_paths: z.array(z.string()).optional(),
  conflicts: z.array(z.unknown()),
  required_evidence: z.array(z.string()),
  compute_budget: z.object({
    cpu_minutes: z.number().int(),
    environment_minutes: z.number().int(),
    repair_attempts: z.number().int(),
  }),
  queue_position: z.number().int().nullable().optional(),
  eta_seconds: z.number().int().nullable().optional(),
});
export type IntentGrant = z.infer<typeof intentGrantSchema>;

export const candidateAcceptedSchema = z.object({
  candidate_id: z.string().regex(/^cand_[0-9A-HJKMNP-TV-Z]{26}$/),
  plan_summary: z.object({ tiers: z.array(z.unknown()).optional(), deferred: z.array(z.string()).optional() }).passthrough(),
  lease_id: z.string(),
});
export type CandidateAccepted = z.infer<typeof candidateAcceptedSchema>;

export const dossierSchema = z.object({
  candidate_id: z.string(),
  intent_id: z.string(),
  generated_at: z.string(),
  inputs_hash: z.string(),
  decision: z.object({
    decision_id: z.string(),
    verb: z.enum(['eligible_for_merge_train', 'rejected', 'deferred']),
    confidence: z.number(),
    policy: z.object({ policy_id: z.string(), version: z.number().int() }),
    summary: z.string(),
  }),
  evidence_accepted: z.array(
    z.object({
      ev_id: z.string(),
      kind: z.string(),
      verdict: z.enum(['pass', 'fail']),
      digests: z.array(z.string()).optional(),
      meta: z.record(z.unknown()).optional(),
    }),
  ),
  evidence_deferred: z.array(z.object({ kind: z.string(), reason: z.string(), stage_required: z.string() })),
  known_uncertainty: z.array(z.record(z.string())),
  required_post_merge: z.array(z.record(z.unknown())),
});
export type Dossier = z.infer<typeof dossierSchema>;

export const leaseRenewalSchema = z.object({
  lease_id: z.string(),
  ttl_expires_at: z.string(),
  renewal_count: z.number().int(),
});
export type LeaseRenewal = z.infer<typeof leaseRenewalSchema>;

export const eventEnvelopeZodSchema = z.object({
  id: z.string().regex(/^evt_[0-9A-HJKMNP-TV-Z]{26}$/),
  seq: z.number().int().min(1),
  type: z.string(),
  version: z.literal(1),
  tenant_id: z.string().regex(/^[a-z]+_[0-9A-HJKMNP-TV-Z]{26}$/),
  aggregate: z.object({ type: z.string(), id: z.string() }),
  causation_id: z.string(),
  correlation_id: z.string(),
  actor: z.object({ kind: z.enum(['agent', 'human', 'service', 'github']), id: z.string() }),
  occurred_at: z.string(),
  payload_sha256: z.string().regex(/^sha256:[a-f0-9]{64}$/),
  prev_hash: z.string().regex(/^sha256:[a-f0-9]{64}$/),
  entry_hash: z.string().regex(/^sha256:[a-f0-9]{64}$/),
  payload: z.record(z.unknown()),
});
export type LedgerEvent = z.infer<typeof eventEnvelopeZodSchema>;

export const eventsTailSchema = z.object({
  events: z.array(eventEnvelopeZodSchema),
  next_seq: z.number().int().min(0),
});
export type EventsTail = z.infer<typeof eventsTailSchema>;

/** internal-protocols.md §2 — fleet execution protocol bodies. */
export const fleetClaimResponseSchema = z.object({
  jobs: z.array(
    z.object({
      run_id: z.string().regex(/^run_[0-9A-HJKMNP-TV-Z]{26}$/),
      attempt: z.number().int().min(1),
      fence_token: z.number().int().min(1),
      tier: z.number().int(),
      pool: z.string(),
      // P0-1/B2: the dispatch-time credential handed to the claiming worker.
      lease_token: z.string().optional(),
      job_spec: z.object({
        kind: z.string(),
        repo: z.string(),
        base_sha: z.string(),
        head_sha: z.string(),
        patch_ref: z.string(),
        inputs_hash: z.string(),
        timeout_ms: z.number().int(),
      }),
    }),
  ),
});
export type FleetClaimResponse = z.infer<typeof fleetClaimResponseSchema>;

export const fleetCompleteRejectionSchema = z.object({
  accepted: z.literal(false),
  reason: z.enum(['fence_mismatch', 'already_accepted']),
});
