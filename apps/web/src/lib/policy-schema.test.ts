import { describe, expect, it } from 'vitest';
import {
  activePoliciesResponseSchema,
  autonomySemantics,
  classifyAutonomy,
  tenantBudgetLimits,
} from './policy-schema';

const validBody = {
  required_evidence_by_risk: {
    low: ['hermetic_build', 'selected_unit'],
    high: ['hermetic_build', 'payment_contract'],
  },
  budgets: {
    per_candidate: { cpu_minutes: 120, repair_attempts: 2 },
    per_tenant_hour: { cpu_minutes: 5000, concurrent_candidates: 40 },
  },
  autonomy: {
    level: 0,
    levels_semantics: { '0': 'observe and explain only', '4': 'mark low-risk merge-eligible' },
    escalate_on: ['security_policy_violation'],
  },
};

const activePolicy = {
  policy_id: 'pol_default',
  version: 1,
  status: 'active' as const,
  activated_at: '2026-08-23T00:00:00Z',
  body: validBody,
};

describe('activePoliciesResponseSchema (G5)', () => {
  it('accepts the §8 policy body', () => {
    const parsed = activePoliciesResponseSchema.safeParse({ policies: [activePolicy] });
    expect(parsed.success).toBe(true);
  });

  it('rejects a body missing the autonomy block (shadow detection depends on it)', () => {
    const { autonomy: _omitted, ...broken } = validBody;
    expect(
      activePoliciesResponseSchema.safeParse({
        policies: [{ ...activePolicy, body: broken }],
      }).success,
    ).toBe(false);
  });

  it('rejects autonomy levels outside 0..6 and non-integer levels', () => {
    for (const level of [7, -1, 2.5]) {
      const broken = { ...validBody, autonomy: { ...validBody.autonomy, level } };
      expect(
        activePoliciesResponseSchema.safeParse({ policies: [{ ...activePolicy, body: broken }] })
          .success,
      ).toBe(false);
    }
  });

  it('rejects a policy without required_evidence_by_risk', () => {
    const { required_evidence_by_risk: _omitted, ...broken } = validBody;
    expect(
      activePoliciesResponseSchema.safeParse({
        policies: [{ ...activePolicy, body: broken }],
      }).success,
    ).toBe(false);
  });

  it('accepts an empty installations-style empty list (graceful absence)', () => {
    expect(activePoliciesResponseSchema.safeParse({ policies: [] }).success).toBe(true);
  });
});

describe('classifyAutonomy (shadow-mode heuristic)', () => {
  const atLevel = (
    level: number,
    status: 'active' | 'retired' = 'active',
  ) => ({
    ...activePolicy,
    status,
    body: { ...validBody, autonomy: { ...validBody.autonomy, level } },
  });

  it('reads shadow when any active policy observes at level 0', () => {
    expect(classifyAutonomy([atLevel(0)])).toBe('shadow');
    expect(classifyAutonomy([atLevel(3), atLevel(0)])).toBe('shadow');
  });

  it('reads enforced when all active policies are level ≥1', () => {
    expect(classifyAutonomy([atLevel(3)])).toBe('enforced');
  });

  it('ignores retired versions when deciding', () => {
    expect(classifyAutonomy([atLevel(3), atLevel(0, 'retired')])).toBe('enforced');
  });

  it('reports unknown with no actives (banner hides gracefully)', () => {
    expect(classifyAutonomy([])).toBe('unknown');
    expect(classifyAutonomy([atLevel(0, 'retired')])).toBe('unknown');
  });
});

describe('tenantBudgetLimits (narrowing the opaque budgets blob)', () => {
  it('extracts per_tenant_hour numbers when present', () => {
    const limits = tenantBudgetLimits(validBody.budgets);
    expect(limits).toEqual({ cpuMinutes: 5000, concurrentCandidates: 40 });
  });

  it('returns nulls for absent or malformed budget blobs', () => {
    expect(tenantBudgetLimits(undefined)).toEqual({ cpuMinutes: null, concurrentCandidates: null });
    expect(tenantBudgetLimits({})).toEqual({ cpuMinutes: null, concurrentCandidates: null });
    expect(tenantBudgetLimits({ per_tenant_hour: 'nope' })).toEqual({
      cpuMinutes: null,
      concurrentCandidates: null,
    });
  });
});

describe('autonomySemantics', () => {
  it('cites the published semantics for the current level', () => {
    expect(autonomySemantics(validBody.autonomy)).toBe('observe and explain only');
  });

  it('falls back to an honest placeholder when semantics are missing', () => {
    expect(autonomySemantics({ level: 5 })).toBe('level 5 (no semantics published)');
  });
});
