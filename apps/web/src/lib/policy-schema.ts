// G5 GET /v1/policies/active (PRODUCT_UX_PLAN §6): active versions + full §8
// body. The autonomy block is REQUIRED — shadow-mode surfacing (T5) depends on
// a trustworthy level, so a policy without one fails validation rather than
// silently reading as "enforced".
import { z } from 'zod';
import { riskClassSchema } from './api-schemas';

export const autonomyBlockSchema = z.object({
  level: z.number().int().min(0).max(6),
  levels_semantics: z.record(z.string()).optional(),
  auto_merge_risk_classes: z.array(z.string()).optional(),
  auto_repair_classes: z.array(z.string()).optional(),
  escalate_on: z.array(z.string()).optional(),
});
export type AutonomyBlock = z.infer<typeof autonomyBlockSchema>;

export const scopeSelectorsSchema = z.object({
  repos: z.array(z.string()).optional(),
  paths: z.array(z.string()).optional(),
  risk_classes: z.array(riskClassSchema).optional(),
});

export const ladderOverridesSchema = z.object({
  tier3_risk_classes: z.array(riskClassSchema).optional(),
  min_selection_confidence: z.number().min(0).max(1).optional(),
  fallback_triggers: z.array(z.string()).optional(),
  protected_paths: z.array(z.string()).optional(),
});

// Budget sub-shapes vary per category (per_candidate / wip_by_tier / …); kept
// opaque here and narrowed at the single read site (tenantBudgetLimits).
export const policyBodySchema = z.object({
  scope_selectors: scopeSelectorsSchema.optional(),
  required_evidence_by_risk: z.record(z.array(z.string())),
  ladder_overrides: ladderOverridesSchema.optional(),
  budgets: z.record(z.unknown()).optional(),
  autonomy: autonomyBlockSchema,
});
export type PolicyBody = z.infer<typeof policyBodySchema>;

export const activePolicySchema = z.object({
  policy_id: z.string(),
  version: z.number().int().positive(),
  status: z.enum(['active', 'retired']),
  activated_at: z.string().optional(),
  activated_by: z.string().optional(),
  body: policyBodySchema,
});
export const activePoliciesResponseSchema = z.object({
  policies: z.array(activePolicySchema),
});

export type ActivePolicy = z.infer<typeof activePolicySchema>;
export type ActivePoliciesResponse = z.infer<typeof activePoliciesResponseSchema>;

export type AutonomyLevelStatus = 'shadow' | 'enforced' | 'unknown';

// v0.2 heuristic: any ACTIVE policy observing at level 0 ⇒ shadow mode.
// Retired versions never re-shadow the tenant; no actives ⇒ unknown (banner
// hides — graceful absence until G5 lands).
export function classifyAutonomy(policies: ActivePolicy[]): AutonomyLevelStatus {
  const active = policies.filter((policy) => policy.status === 'active');
  if (active.length === 0) return 'unknown';
  return active.some((policy) => policy.body.autonomy.level === 0)
    ? 'shadow'
    : 'enforced';
}

const tenantHourBudgetSchema = z
  .object({
    cpu_minutes: z.number(),
    concurrent_candidates: z.number(),
  })
  .partial();

export interface TenantBudgetLimits {
  cpuMinutes: number | null;
  concurrentCandidates: number | null;
}

// Immediate narrowing of the opaque budgets blob; the full budget strip ships
// with G7/U-W4, posture review only cites the tenant-hour numbers.
export function tenantBudgetLimits(
  budgets: Record<string, unknown> | undefined,
): TenantBudgetLimits {
  if (!budgets) return { cpuMinutes: null, concurrentCandidates: null };
  const parsed = tenantHourBudgetSchema.safeParse(budgets.per_tenant_hour);
  if (!parsed.success) return { cpuMinutes: null, concurrentCandidates: null };
  return {
    cpuMinutes: parsed.data.cpu_minutes ?? null,
    concurrentCandidates: parsed.data.concurrent_candidates ?? null,
  };
}

// Human-readable semantics line for the current level ("0" → observe/explain).
export function autonomySemantics(
  block: AutonomyBlock,
): string {
  const semantics = block.levels_semantics?.[String(block.level)];
  return semantics ?? `level ${block.level} (no semantics published)`;
}
