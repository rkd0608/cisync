import { z } from 'zod';

// Boundary schemas for every shape in packages/contracts/openapi.yaml v1.0.0.
// The API client fail-closes: any response that does not parse is routed to an
// error state, never rendered.

export const RISK_CLASSES = ['low', 'medium', 'high', 'critical'] as const;
export const INTENT_STATES = [
  'exploring',
  'validating',
  'blocked',
  'repairing',
  'merge_ready',
  'deploying',
  'monitoring',
  'completed',
  'rejected',
] as const;
export const CANDIDATE_RELATIONS = [
  'duplicate_of',
  'alternative_of',
  'composable_with',
  'conflicts_with',
  'prerequisite_of',
] as const;
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

export const riskClassSchema = z.enum(RISK_CLASSES);
export const intentStateSchema = z.enum(INTENT_STATES);
export const relationSchema = z.enum(CANDIDATE_RELATIONS);

export const intentIdSchema = z
  .string()
  .regex(/^int_[0-9A-HJKMNP-TV-Z]{26}$/, 'malformed intent id');
export const candidateIdSchema = z
  .string()
  .regex(/^cand_[0-9A-HJKMNP-TV-Z]{26}$/, 'malformed candidate id');
export const clusterIdSchema = z
  .string()
  .regex(/^clus_[0-9A-HJKMNP-TV-Z]{26}$/, 'malformed cluster id');
export const decisionIdSchema = z
  .string()
  .regex(/^dec_[0-9A-HJKMNP-TV-Z]{26}$/, 'malformed decision id');

export const errorEnvelopeSchema = z.object({
  error: z.object({
    code: z.enum(ERROR_CODES),
    message: z.string(),
    details: z.record(z.unknown()).optional(),
    retry_after_s: z.number().int().nullable().optional(),
    suggestions: z.array(z.string()).optional(),
  }),
});

export const computeBudgetSchema = z.object({
  cpu_minutes: z.number().int(),
  environment_minutes: z.number().int(),
  repair_attempts: z.number().int(),
});

export const conflictRefSchema = z.object({
  intent_id: z.string(),
  relation: z.literal('overlapping'),
  owner: z.string(),
  recommendation: z.enum(['coordinate', 'proceed', 'wait']),
});

export const intentGrantSchema = z.object({
  intent_id: z.string(),
  lease_id: z.string(),
  base_snapshot: z.string(),
  worktree_or_branch: z.string(),
  allowed_paths: z.array(z.string()),
  prohibited_paths: z.array(z.string()).optional(),
  conflicts: z.array(conflictRefSchema),
  required_evidence: z.array(z.string()),
  compute_budget: computeBudgetSchema,
  queue_position: z.number().int().nullable().optional(),
  eta_seconds: z.number().int().nullable().optional(),
});

export const intentSchema = z.object({
  intent_id: z.string(),
  state: intentStateSchema,
  goal: z.string(),
  repository: z.string(),
  owned_surfaces: z.array(z.string()),
  risk_class: riskClassSchema,
  evidence_completeness_pct: z.number().min(0).max(1),
  deadline: z.string().nullable().optional(),
  created_at: z.string(),
  closed_at: z.string().nullable().optional(),
});

export const candidateSummarySchema = z.object({
  candidate_id: z.string(),
  state: z.string(),
  head_sha: z.string(),
  priority_score: z.number().optional(),
  cluster_id: z.string().nullable(),
  relation_to_rep: relationSchema.nullable(),
});

export const planTierSchema = z.object({
  tier: z.number().int().min(0).max(4),
  jobs: z.array(z.string()).optional(),
  rationale: z.string().optional(),
  selection_confidence: z.number().nullable().optional(),
});

export const planSummarySchema = z.object({
  tiers: z.array(planTierSchema).optional(),
  deferred: z.array(z.string()).optional(),
});

export const candidateAcceptedSchema = z.object({
  candidate_id: z.string(),
  plan_summary: planSummarySchema,
  lease_id: z.string(),
});

export const candidateSchema = candidateSummarySchema.extend({
  intent_id: z.string(),
  queue_position: z.number().int().nullable(),
  est_cost_millicents: z.number().int(),
});

// Decision.explanation.factors (DOMAIN_MODEL_DRAFT §7): name/value/source
// triples rendered verbatim in the decision banner (T1 — no UI-invented
// rationale). Optional: dossiers from before the field lands still parse.
export const decisionFactorSchema = z.object({
  name: z.string(),
  value: z.union([z.string(), z.number(), z.boolean()]),
  source: z.string(),
});

export const decisionSchema = z.object({
  decision_id: z.string(),
  verb: z.enum(['eligible_for_merge_train', 'rejected', 'deferred']),
  confidence: z.number(),
  policy: z
    .object({
      policy_id: z.string().optional(),
      version: z.number().int().optional(),
    })
    .optional(),
  summary: z.string(),
  explanation: z
    .object({
      factors: z.array(decisionFactorSchema).optional(),
    })
    .optional(),
});

export const evidenceAcceptedSchema = z.object({
  ev_id: z.string(),
  kind: z.string(),
  verdict: z.enum(['pass', 'fail']),
  digests: z.array(z.string()).optional(),
  meta: z.record(z.unknown()).optional(),
});

export const evidenceDeferredSchema = z.object({
  kind: z.string(),
  reason: z.string(),
  stage_required: z.string(),
});

export const knownUncertaintyItemSchema = z.object({
  description: z.string().optional(),
  mitigation: z.string().optional(),
});

export const postMergeItemSchema = z.object({
  kind: z.string().optional(),
  params: z.record(z.unknown()).optional(),
});

// Mirrors DOMAIN_MODEL_DRAFT §7 exactly: deferred/uncertainty/post_merge are
// mandatory sections (possibly empty), never optional.
export const evidenceDossierSchema = z.object({
  candidate_id: z.string(),
  intent_id: z.string(),
  generated_at: z.string(),
  inputs_hash: z.string(),
  decision: decisionSchema,
  evidence_accepted: z.array(evidenceAcceptedSchema),
  evidence_deferred: z.array(evidenceDeferredSchema),
  known_uncertainty: z.array(knownUncertaintyItemSchema),
  required_post_merge: z.array(postMergeItemSchema),
});

export const clusterMemberSchema = z.object({
  candidate_id: z.string(),
  relation_to_rep: relationSchema.nullable(),
  similarity_score: z.number().optional(),
});

export const clusterSchema = z.object({
  cluster_id: z.string(),
  repo: z.string(),
  rep_candidate_id: z.string(),
  member_count: z.number().int().optional(),
  state: z.enum(['forming', 'active', 'dissolved']),
  strategy_version: z.string(),
  members: z.array(clusterMemberSchema),
});

export const leaseRenewalSchema = z.object({
  lease_id: z.string(),
  ttl_expires_at: z.string(),
  renewal_count: z.number().int(),
});

export type ConflictRef = z.infer<typeof conflictRefSchema>;
export type Decision = z.infer<typeof decisionSchema>;
export type IntentGrant = z.infer<typeof intentGrantSchema>;
export type Intent = z.infer<typeof intentSchema>;
export type CandidateSummary = z.infer<typeof candidateSummarySchema>;
export type Candidate = z.infer<typeof candidateSchema>;
export type EvidenceDossier = z.infer<typeof evidenceDossierSchema>;
export type Cluster = z.infer<typeof clusterSchema>;
export type ClusterMember = z.infer<typeof clusterMemberSchema>;
export type DecisionFactor = z.infer<typeof decisionFactorSchema>;
export type ErrorEnvelope = z.infer<typeof errorEnvelopeSchema>;
