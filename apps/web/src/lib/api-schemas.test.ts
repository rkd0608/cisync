import { describe, expect, it } from 'vitest';
import { candidateSchema, clusterSchema, errorEnvelopeSchema, evidenceDossierSchema, intentGrantSchema, intentIdSchema, intentSchema } from './api-schemas';
import { eventEnvelopeSchema, eventsPageSchema } from './event-schemas';

const validIntent = {
  intent_id: 'int_01J8ZC7XK9Q2W3E4R5T6Y70001',
  state: 'validating',
  goal: 'Add idempotency keys to checkout',
  repository: 'acme/payments',
  owned_surfaces: ['services/checkout/**'],
  risk_class: 'high',
  evidence_completeness_pct: 0.6,
  deadline: null,
  created_at: '2026-08-23T03:00:00Z',
};

describe('intentSchema', () => {
  it('accepts a conforming Intent', () => {
    expect(intentSchema.safeParse(validIntent).success).toBe(true);
  });

  it('rejects missing evidence_completeness_pct (D8 field)', () => {
    const { evidence_completeness_pct: _omitted, ...rest } = validIntent;
    expect(intentSchema.safeParse(rest).success).toBe(false);
  });

  it('rejects completeness outside 0..1', () => {
    expect(
      intentSchema.safeParse({ ...validIntent, evidence_completeness_pct: 1.5 }).success,
    ).toBe(false);
    expect(
      intentSchema.safeParse({ ...validIntent, evidence_completeness_pct: -0.1 }).success,
    ).toBe(false);
  });

  it('rejects unknown state values', () => {
    expect(intentSchema.safeParse({ ...validIntent, state: 'flying' }).success).toBe(false);
  });
});

describe('errorEnvelopeSchema', () => {
  it('accepts the uniform cross-tenant 404 body', () => {
    const body = { error: { code: 'not_found', message: 'resource not found' } };
    const parsed = errorEnvelopeSchema.safeParse(body);
    expect(parsed.success).toBe(true);
    if (parsed.success) expect(parsed.data.error.code).toBe('not_found');
  });

  it('accepts budget_exceeded with details and retry hint', () => {
    const body = {
      error: {
        code: 'budget_exceeded',
        message: 'tenant cpu-minute budget exhausted',
        details: { scope: 'tenant:org_01J', kind: 'cpu_minutes', limit: 5000 },
        retry_after_s: 600,
        suggestions: ['reduce expected_surfaces'],
      },
    };
    expect(errorEnvelopeSchema.safeParse(body).success).toBe(true);
  });

  it('rejects codes outside the v1 enum', () => {
    expect(
      errorEnvelopeSchema.safeParse({ error: { code: 'explode', message: 'x' } }).success,
    ).toBe(false);
  });
});

describe('evidenceDossierSchema', () => {
  const dossier = {
    candidate_id: 'cand_01J8ZC7XK9Q2W3E4R5T6Y70002',
    intent_id: validIntent.intent_id,
    generated_at: '2026-08-23T03:41:00Z',
    inputs_hash: 'sha256:9f1c',
    decision: {
      decision_id: 'dec_01J8ZC7XK9Q2W3E4R5T6Y70003',
      verb: 'eligible_for_merge_train',
      confidence: 0.94,
      policy: { policy_id: 'pol_payments_high_risk', version: 4 },
      summary: 'Eligible; browser E2E deferred to canary.',
    },
    evidence_accepted: [
      { ev_id: 'ev_4482', kind: 'hermetic_build', verdict: 'pass' },
      { ev_id: 'ev_4490', kind: 'selected_unit', verdict: 'pass', meta: { selected: 44 } },
    ],
    evidence_deferred: [
      { kind: 'browser_e2e', reason: 'no reachable UI path', stage_required: 'canary' },
    ],
    known_uncertainty: [{ description: 'sandbox gap', mitigation: '2% canary' }],
    required_post_merge: [{ kind: 'canary', params: { traffic_pct: 2 } }],
  };

  it('accepts the §7 shape', () => {
    expect(evidenceDossierSchema.safeParse(dossier).success).toBe(true);
  });

  it('rejects a dossier missing mandatory deferred section', () => {
    const { evidence_deferred: _omitted, ...rest } = dossier;
    expect(evidenceDossierSchema.safeParse(rest).success).toBe(false);
  });

  it('rejects a deferred item without its reason', () => {
    const broken = {
      ...dossier,
      evidence_deferred: [{ kind: 'browser_e2e', stage_required: 'canary' }],
    };
    expect(evidenceDossierSchema.safeParse(broken).success).toBe(false);
  });

  it('allows empty mandatory sections (possibly empty is still required)', () => {
    const empty = {
      ...dossier,
      evidence_accepted: [],
      known_uncertainty: [],
      required_post_merge: [],
    };
    expect(evidenceDossierSchema.safeParse(empty).success).toBe(true);
  });
});

describe('eventEnvelopeSchema / eventsPageSchema', () => {
  const envelope = {
    id: 'evt_01J8ZC7XK9Q2W3E4R5T6Y70006',
    seq: 42,
    type: 'intent.declared',
    version: 1,
    tenant_id: 'org_01J',
    aggregate: { type: 'intent', id: validIntent.intent_id },
    causation_id: 'evt_0',
    correlation_id: 'corr_1',
    actor: { kind: 'agent', id: 'agent:checkout' },
    occurred_at: '2026-08-23T03:00:00Z',
    payload_sha256: 'aa'.repeat(32),
    prev_hash: 'bb'.repeat(32),
    entry_hash: 'cc'.repeat(32),
    payload: { goal: 'x', risk_class: 'low' },
  };

  it('accepts a conforming envelope and page', () => {
    expect(eventEnvelopeSchema.safeParse(envelope).success).toBe(true);
    expect(eventsPageSchema.safeParse({ events: [envelope], next_seq: 43 }).success).toBe(true);
  });

  it('rejects an envelope with a bad id prefix', () => {
    expect(eventEnvelopeSchema.safeParse({ ...envelope, id: 'evt_zzz' }).success).toBe(false);
  });

  it('rejects a page missing next_seq', () => {
    expect(eventsPageSchema.safeParse({ events: [] }).success).toBe(false);
  });
});

describe('candidateSchema / clusterSchema / intentGrantSchema / id patterns', () => {
  const candidate = {
    candidate_id: 'cand_01J8ZC7XK9Q2W3E4R5T6Y70004',
    state: 'validating',
    head_sha: 'a'.repeat(40),
    cluster_id: null,
    relation_to_rep: null,
    intent_id: validIntent.intent_id,
    queue_position: 3,
    est_cost_millicents: 1200,
  };

  it('accepts candidate with nullable cluster/relation', () => {
    expect(candidateSchema.safeParse(candidate).success).toBe(true);
  });

  it('rejects invalid relation enum values', () => {
    expect(
      candidateSchema.safeParse({ ...candidate, relation_to_rep: 'besties_with' }).success,
    ).toBe(false);
  });

  it('accepts cluster with optional member similarity', () => {
    const cluster = {
      cluster_id: 'clus_01J8ZC7XK9Q2W3E4R5T6Y70006',
      repo: 'acme/payments',
      rep_candidate_id: candidate.candidate_id,
      member_count: 2,
      state: 'active',
      strategy_version: 'trigram-v0',
      members: [
        {
          candidate_id: candidate.candidate_id,
          relation_to_rep: null,
          similarity_score: 1.0,
        },
        { candidate_id: 'cand_01J8ZC7XK9Q2W3E4R5T6Y70005', relation_to_rep: 'duplicate_of' },
      ],
    };
    expect(clusterSchema.safeParse(cluster).success).toBe(true);
  });

  it('accepts an IntentGrant with conflicts array', () => {
    const grant = {
      intent_id: validIntent.intent_id,
      lease_id: 'lease_01J8ZC7XK9Q2W3E4R5T6Y70007',
      base_snapshot: 'main@b734e',
      worktree_or_branch: 'agent/int_x/candidate_01',
      allowed_paths: ['services/checkout/**'],
      conflicts: [
        {
          intent_id: 'int_01J8ZC7XK9Q2W3E4R5T6Y70008',
          relation: 'overlapping',
          owner: 'payments-platform',
          recommendation: 'coordinate',
        },
      ],
      required_evidence: ['payment_contract'],
      compute_budget: { cpu_minutes: 120, environment_minutes: 30, repair_attempts: 2 },
    };
    expect(intentGrantSchema.safeParse(grant).success).toBe(true);
  });

  it('enforces ULID-shaped route ids', () => {
    expect(intentIdSchema.safeParse(validIntent.intent_id).success).toBe(true);
    expect(intentIdSchema.safeParse('int_short').success).toBe(false);
    // lowercase ULID chars are rejected by the contract pattern
    expect(intentIdSchema.safeParse(`int_${'a'.repeat(26)}`).success).toBe(false);
  });
});
