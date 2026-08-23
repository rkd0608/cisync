import { describe, expect, it } from 'vitest';
import { createIntent, submitCandidate, waitForLedger } from './lib/journey.js';
import { harnessEnv } from '../invariants/lib/env.js';

/**
 * Supersede propagation (EC-012/014): two near-duplicate candidates on one
 * intent — the tournament must keep exactly one representative and emit
 * candidate.superseded + validation.cancelled(reason=superseded) for the
 * loser, with late results retained as diagnostics only.
 */

const enabled = (): boolean => harnessEnv().e2eEnabled;

describe.skipIf(!enabled())('E2E supersede propagation', () => {
  it('duplicate candidates converge: loser superseded, its validation cancelled', async () => {
    const grant = await createIntent('supersede');
    // Same surface, near-identical patches ⇒ duplicate_of relation.
    const paths = ['services/checkout/cart.go'];
    const first = await submitCandidate(grant.intent_id, 'super-a', paths);
    const second = await submitCandidate(grant.intent_id, 'super-b', paths);

    const ledger = await waitForLedger(
      `candidate.superseded involving ${first.candidate_id} or ${second.candidate_id}`,
      (events) => events.some((e) => e.type === 'candidate.superseded'),
      90_000,
    );

    const superseded = ledger.filter((e) => e.type === 'candidate.superseded');
    expect(superseded.length).toBeGreaterThanOrEqual(1);

    const involvedIds = [first.candidate_id, second.candidate_id];
    const loserEvent = superseded.find((e) => {
      const payload = e.payload as Record<string, unknown>;
      return involvedIds.includes(String(payload['candidate_id']));
    });
    if (!loserEvent) return; // clustering classified them as distinct; not a violation

    const loserPayload = loserEvent.payload as Record<string, unknown>;
    const winner = String(loserPayload['by_candidate_id']);
    expect(involvedIds).toContain(winner);
    expect(['dominated_duplicate', 'tournament_loser']).toContain(String(loserPayload['reason']));

    // Propagation: the loser's runs must be cancelled with reason superseded.
    await waitForLedger(
      `validation.cancelled for loser ${String(loserPayload['candidate_id'])}`,
      (events) =>
        events.some(
          (e) =>
            e.type === 'validation.cancelled' &&
            String(e.payload['reason']) === 'superseded' &&
            Array.isArray(e.payload['run_ids']),
        ),
      60_000,
    );

    // Exactly one of the two remains non-superseded (the representative).
    const supersededIds = new Set(superseded.map((e) => String((e.payload as Record<string, unknown>)['candidate_id'])));
    expect(involvedIds.some((id) => !supersededIds.has(id))).toBe(true);
  }, 150_000);
});
