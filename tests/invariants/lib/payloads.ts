import { prefixedId, type IdPrefix } from './builders.js';

/**
 * Payload factories for the CORE event set (events.schema.json $defs).
 * Only required fields are set — contract suites then prove the SCHEMA
 * accepts them and rejects adversarial mutations.
 */

const SHA = (seed: string): string => {
  let hex = '';
  for (let i = 0; i < 64; i++) hex += ((seed.charCodeAt(i % seed.length) + i) % 16).toString(16);
  return `sha256:${hex}`;
};
const SHA40 = (seed: string): string => SHA(seed).slice(7, 47);

export function id(prefix: IdPrefix, seed: string): string {
  return prefixedId(prefix, seed);
}

export const policyRef = (seed: string): { policy_id: string; policy_version: number } => ({
  policy_id: `default-pack-${seed}`,
  policy_version: 1,
});

export const budget = (seed: string): { cpu_minutes: number; environment_minutes: number; repair_attempts: number } => ({
  cpu_minutes: 100,
  environment_minutes: 200,
  repair_attempts: 3 - (seed.length % 2),
});

export const intentDeclared = (seed: string): Record<string, unknown> => ({
  intent_id: id('int', seed),
  goal: `synthetic goal ${seed}`,
  owned_surfaces: ['services/checkout/**'],
  risk_class: 'medium',
  origin: 'synthetic',
  resolved_policy: policyRef(seed),
  compute_budget: budget(seed),
});

export const leaseGranted = (seed: string): Record<string, unknown> => ({
  lease_id: id('lease', seed),
  intent_id: id('int', seed),
  scope: { kind: 'change_scope', surfaces: ['services/checkout/**'] },
  holder: `agent-${seed}`,
  budget: budget(seed),
  ttl_expires_at: '2026-08-23T01:00:00.000Z',
  conflicts: [],
  required_evidence: ['selected_unit_pass'],
});

export const candidateSubmitted = (seed: string): Record<string, unknown> => ({
  candidate_id: id('cand', seed),
  intent_id: id('int', seed),
  submitter: `agent-${seed}`,
  patch_ref: `bundle:${SHA(seed).slice(7)}`,
  head_sha: SHA40(`head-${seed}`),
  base_sha: SHA40(`base-${seed}`),
  changed_paths: ['services/checkout/cart.go'],
});

export const validationPlanned = (seed: string): Record<string, unknown> => ({
  plan_id: id('val', seed),
  candidate_id: id('cand', seed),
  tiers: [{ tier: 1, jobs: ['selected_unit'], rationale: 'static selection' }],
  required_evidence_kinds: ['selected_unit_pass'],
  inputs_hash: SHA(`inputs-${seed}`),
  policy_version: policyRef(seed),
});

export const validationRequested = (seed: string): Record<string, unknown> => ({
  run_id: id('run', seed),
  plan_id: id('val', seed),
  candidate_id: id('cand', seed),
  tier: 1,
  est_cost_millicents: 120,
  priority: 0.5,
  pool: 'sim',
});

export const validationStarted = (seed: string, fenceToken = 1): Record<string, unknown> => ({
  run_id: id('run', seed),
  attempt: 1,
  fence_token: fenceToken,
  worker_id: `worker-${seed}`,
  provider: 'sim',
});

export const validationCompleted = (seed: string, status = 'succeeded'): Record<string, unknown> => ({
  run_id: id('run', seed),
  attempt: 1,
  status,
  duration_ms: 800,
});

export const evidenceRecorded = (seed: string, verdict: 'pass' | 'fail' = 'pass'): Record<string, unknown> => ({
  ev_id: id('ev', seed),
  run_id: id('run', seed),
  attempt: 1,
  candidate_id: id('cand', seed),
  kind: 'selected_unit_pass',
  verdict,
  digests: [SHA(`artifact-${seed}`)],
  inputs_hash: SHA(`inputs-${seed}`),
  cost_millicents: 120,
  produced_by_lease: id('lease', seed),
});

export const decisionRendered = (seed: string, verb = 'eligible_for_merge_train'): Record<string, unknown> => ({
  decision_id: id('dec', seed),
  subject: { type: 'candidate', id: id('cand', seed) },
  verb,
  confidence: 0.93,
  policy: policyRef(seed),
  explanation: { summary: `decision ${seed}`, factors: [{ name: 'evidence', value: 1, source: 'dossier' }] },
  evidence_refs: [id('ev', seed)],
  inputs_hash: SHA(`inputs-${seed}`),
});

export const deliveryAccepted = (guidSeed: string): Record<string, unknown> => ({
  source: 'github',
  ext_delivery_id: `delivery-guid-${guidSeed}`,
  normalized_kind: 'push',
  repo: `acme/repo-${guidSeed}`,
});

export const candidateSuperseded = (loserSeed: string, winnerSeed: string): Record<string, unknown> => ({
  candidate_id: id('cand', loserSeed),
  by_candidate_id: id('cand', winnerSeed),
  relation: 'duplicate_of',
  reason: 'dominated_duplicate',
});

export const validationCancelled = (seed: string, reason = 'superseded'): Record<string, unknown> => ({
  run_ids: [id('run', seed)],
  reason,
});

export const mergeBaseAdvanced = (repoSeed: string, candidates: string[]): Record<string, unknown> => ({
  repo: `acme/repo-${repoSeed}`,
  old_sha: SHA40(`old-base-${repoSeed}`),
  new_sha: SHA40(`new-base-${repoSeed}`),
  affected_candidate_ids: candidates.map((c) => id('cand', c)),
});
