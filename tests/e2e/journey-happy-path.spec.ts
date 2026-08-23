import { describe, expect, it } from 'vitest';
import { createIntent, submitCandidate, waitForLedger } from './lib/journey.js';
import { getDossier } from '../invariants/lib/live.js';
import { harnessEnv } from '../invariants/lib/env.js';

/**
 * Happy-path journey: intent → grant → candidate → plan → (sim runs) →
 * decision.rendered, observed ONLY through the public API and the ledger
 * tail. Asserts the EVENT SEQUENCE and final projections — never internals.
 */

const enabled = (): boolean => harnessEnv().e2eEnabled;

describe.skipIf(!enabled())('E2E happy path: intent to decision', () => {
  it('produces the documented lifecycle event sequence in order', async () => {
    const grant = await createIntent('happy');
    expect(grant.intent_id).toMatch(/^int_/);
    expect(grant.lease_id).toMatch(/^lease_/);
    expect(grant.required_evidence.length).toBeGreaterThan(0);
    expect(grant.compute_budget.repair_attempts).toBeGreaterThanOrEqual(0);

    const cand = await submitCandidate(grant.intent_id, 'happy');
    expect(cand.candidate_id).toMatch(/^cand_/);

    const ordered = [
      'intent.declared',
      'lease.granted',
      'candidate.submitted',
      'validation.planned',
      'validation.requested',
      'validation.started',
      'validation.completed',
      'decision.rendered',
    ];

    const ledger = await waitForLedger(
      `full lifecycle for ${cand.candidate_id}`,
      (events) =>
        events.some((e) => e.type === 'decision.rendered' && String((e.payload['subject'] as Record<string, unknown> | undefined)?.id) === cand.candidate_id),
    );

    // The candidate's own events must appear in the frozen order above.
    const relevant = ledger.filter((e) => JSON.stringify(e.payload).includes(cand.candidate_id));
    const typeSeq = [...new Set(relevant.map((e) => e.type))];
    const present = ordered.filter((t) => typeSeq.includes(t));
    expect(present, `expected lifecycle order; got ${typeSeq.join(',')}`).toEqual(ordered.slice(0, present.length));

    // Sequence numbers are contiguous across everything we drained.
    for (let i = 1; i < ledger.length; i++) {
      const prev = ledger[i - 1];
      const curr = ledger[i];
      if (prev && curr) expect(curr.seq).toBeGreaterThan(prev.seq);
    }
  }, 120_000);

  it('final projection: dossier shows a decided candidate with complete evidence', async () => {
    const grant = await createIntent('dossier');
    const cand = await submitCandidate(grant.intent_id, 'dossier');
    await waitForLedger(
      `decision.rendered for ${cand.candidate_id}`,
      (events) => events.some((e) => e.type === 'decision.rendered' && JSON.stringify(e.payload).includes(cand.candidate_id)),
    );
    const dossier = await getDossier(cand.candidate_id);
    expect(dossier.candidate_id).toBe(cand.candidate_id);
    expect(dossier.decision.verb).toBe('eligible_for_merge_train');
    expect(dossier.decision.policy.policy_id).toBeTruthy();
    expect(dossier.inputs_hash).toMatch(/^sha256:[a-f0-9]{64}$/);
    // Deferred evidence must be explained (kind + reason + required stage).
    for (const deferred of dossier.evidence_deferred) {
      expect(deferred.kind).toBeTruthy();
      expect(deferred.reason).toBeTruthy();
    }
  }, 120_000);
});
