import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { hashInputs, materialsEqualForReuse, type InputsMaterial } from './lib/inputs-hash.js';
import { inputsHashCollisionAttempt } from './lib/attack-vectors.js';
import { envelopeForType } from './lib/event-schema.js';
import * as payloads from './lib/payloads.js';
import { buildChainedEnvelopes, firstEnvelope } from './lib/builders.js';
import { liveModeEnabled } from './lib/env.js';
import { createIntent, submitCandidate, tailEvents, waitForEvent } from './lib/live.js';

/**
 * I-02 — Evidence/artifact reuse only on FULL inputs_hash match
 * (base SHA, lockfiles, flags, toolchain).
 * Contract mode: property-prove digest sensitivity (any single-field change
 * ⇒ different key) and that schema requires the stamp on plans/evidence.
 * Live mode: same head_sha under a moved base MUST produce a new inputs_hash.
 */

const arbitraryMaterial: fc.Arbitrary<InputsMaterial> = fc.record({
  baseSha: fc.hexaString({ minLength: 40, maxLength: 40 }),
  lockfiles: fc.array(fc.string({ minLength: 1, maxLength: 20 }), { maxLength: 4 }),
  flags: fc.array(fc.constantFrom('--race', '-count=1', '--cover', '-tags=e2e'), { maxLength: 4 }),
  toolchain: fc.constantFrom('go1.23', 'node22', 'py312'),
});

describe('I-02 contract: reuse key is fully input-sensitive and order-insensitive', () => {
  it('any single-field mutation changes the digest (changed input ⇒ miss)', () => {
    fc.assert(
      fc.property(arbitraryMaterial, inputsHashCollisionAttempt, (base, attempt) => {
        expect(hashInputs(attempt.first)).not.toBe(hashInputs(attempt.second));
        expect(materialsEqualForReuse(attempt.first, attempt.second)).toBe(false);
        // Order of slice fields must not matter for equality or digest.
        const shuffled: InputsMaterial = {
          ...base,
          lockfiles: [...base.lockfiles].reverse(),
          flags: [...base.flags].reverse(),
        };
        expect(hashInputs(shuffled)).toBe(hashInputs(base));
      }),
    );
  });

  it('identical materials in different submission order share one key', () => {
    fc.assert(
      fc.property(arbitraryMaterial, (m) => {
        const reordered: InputsMaterial = {
          baseSha: m.baseSha,
          toolchain: m.toolchain,
          lockfiles: [...m.lockfiles],
          flags: [...m.flags],
        };
        expect(hashInputs(m)).toBe(hashInputs(reordered));
      }),
    );
  });

  it('schema rejects validation.planned without an inputs_hash stamp', () => {
    const missing = structuredClone(payloads.validationPlanned('i02'));
    delete (missing as Record<string, unknown>)['inputs_hash'];
    const env = firstEnvelope(buildChainedEnvelopes([
      { seq: 1, type: 'validation.planned', aggregate: { type: 'validation_plan', id: payloads.id('val', 'i02') }, payload: missing },
]));
    expect(envelopeForType('validation.planned', env).valid).toBe(false);
  });
});

describe.skipIf(!liveModeEnabled())('I-02 live: moving the base invalidates the plan inputs_hash', () => {
  it('same patch under a different base yields a distinct inputs_hash', async () => {
    const grant = await createIntent('i02-live');
    const first = await submitCandidate(grant.intent_id, 'i02-live-a');
    await waitForEvent(
      (ev) => ev.type === 'validation.planned' && ev.payload['candidate_id'] === first.candidate_id,
      { description: `validation.planned for ${first.candidate_id}` },
    );
    const headSha = String((await eventsFor(first.candidate_id)).find((e) => e.type === 'candidate.submitted')?.payload['head_sha']);
    const second = await submitCandidate(grant.intent_id, 'i02-live-b', { headSha });
    await waitForEvent(
      (ev) => ev.type === 'validation.planned' && ev.payload['candidate_id'] === second.candidate_id,
      { description: `validation.planned for ${second.candidate_id}` },
    );
    const hashes = new Map(
      (await eventsFor(undefined))
        .filter((e) => e.type === 'validation.planned')
        .map((e) => [String(e.payload['candidate_id']), String(e.payload['inputs_hash'])]),
    );
    const h1 = hashes.get(first.candidate_id);
    const h2 = hashes.get(second.candidate_id);
    expect(h1, 'first candidate has no planned inputs_hash').toBeDefined();
    expect(h2, 'second candidate has no planned inputs_hash').toBeDefined();
    expect(h1).not.toBe(h2);

    async function eventsFor(candidateId: string | undefined) {
      const page = await tailEvents(0);
      return page.events.filter((e) => candidateId === undefined || e.payload['candidate_id'] === candidateId);
    }
  });
});
