// Ledger event envelope + per-type payload schemas (openapi EventEnvelope,
// payload bodies from DOMAIN_MODEL_DRAFT §6). Split from api-schemas.ts to
// keep both files under the charter line cap.
import { z } from 'zod';
import { riskClassSchema } from './api-schemas';

export const eventEnvelopeSchema = z.object({
  id: z.string().regex(/^evt_[0-9A-HJKMNP-TV-Z]{26}$/),
  seq: z.number().int(),
  type: z.string(),
  version: z.number().int(),
  tenant_id: z.string(),
  aggregate: z
    .object({ type: z.string().optional(), id: z.string().optional() })
    .optional(),
  causation_id: z.string(),
  correlation_id: z.string(),
  actor: z
    .object({
      kind: z.enum(['agent', 'human', 'service', 'github']).optional(),
      id: z.string().optional(),
    })
    .optional(),
  occurred_at: z.string(),
  payload_sha256: z.string(),
  prev_hash: z.string(),
  entry_hash: z.string(),
  payload: z.record(z.unknown()),
});

export const eventsPageSchema = z.object({
  events: z.array(eventEnvelopeSchema),
  next_seq: z.number().int(),
});

// Per-type payload validators used by the event-board projection.
export const intentDeclaredPayloadSchema = z.object({
  goal: z.string(),
  owned_surfaces: z.array(z.string()).optional(),
  risk_class: riskClassSchema,
  deadline: z.string().nullable().optional(),
});

export const candidateSubmittedPayloadSchema = z.object({
  candidate_id: z.string(),
  intent_id: z.string(),
  head_sha: z.string().optional(),
});

export const decisionRenderedPayloadSchema = z.object({
  decision_id: z.string(),
  subject: z.object({ type: z.string(), id: z.string() }),
  verb: z.enum(['eligible_for_merge_train', 'rejected', 'deferred']),
  confidence: z.number().optional(),
  policy: z
    .object({
      policy_id: z.string().optional(),
      version: z.number().int().optional(),
    })
    .optional(),
});

export type EventEnvelope = z.infer<typeof eventEnvelopeSchema>;
