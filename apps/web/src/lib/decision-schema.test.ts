// decisionSchema boundary tests: the Decision record incl. the optional
// explanation.factors triples (DOMAIN_MODEL_DRAFT §7) rendered by
// decision-banner (T1 — no unattributed rationale ever renders).
import { describe, expect, it } from 'vitest';
import { decisionSchema } from './api-schemas';

const baseDecision = {
  decision_id: 'dec_01J8ZC7XK9Q2W3E4R5T6Y70003',
  verb: 'eligible_for_merge_train',
  confidence: 0.94,
  summary: 'Eligible.',
};

describe('decisionSchema explanation.factors (T1 triples)', () => {
  it('accepts name/value/source factors and tolerates their absence', () => {
    expect(decisionSchema.safeParse(baseDecision).success).toBe(true);
    expect(
      decisionSchema.safeParse({
        ...baseDecision,
        explanation: {
          factors: [{ name: 'selection_confidence', value: 0.987, source: 'learned_stats:v3' }],
        },
      }).success,
    ).toBe(true);
  });

  it('rejects factor values outside string|number|boolean', () => {
    const withFactor = (value: unknown) => ({
      ...baseDecision,
      explanation: { factors: [{ name: 'f', value, source: 's' }] },
    });
    expect(decisionSchema.safeParse(withFactor('high')).success).toBe(true);
    expect(decisionSchema.safeParse(withFactor(2)).success).toBe(true);
    expect(decisionSchema.safeParse(withFactor(false)).success).toBe(true);
    expect(decisionSchema.safeParse(withFactor({ nested: true })).success).toBe(false);
    expect(decisionSchema.safeParse(withFactor(null)).success).toBe(false);
  });

  it('rejects a factor missing its source', () => {
    const broken = {
      ...baseDecision,
      explanation: { factors: [{ name: 'risk_class', value: 'high' }] },
    };
    expect(decisionSchema.safeParse(broken).success).toBe(false);
  });
});
