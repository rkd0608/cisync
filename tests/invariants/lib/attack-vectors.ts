import fc from 'fast-check';
import { buildChainedEnvelopes, prefixedId, TEST_TENANT, OTHER_TENANT, GENESIS_PREV, type EnvelopeLike } from './builders.js';
import * as payloads from './payloads.js';

/**
 * fast-check arbitraries producing WELL-FORMED attack attempts against the
 * documented rules. WHY well-formed: each vector must pass envelope
 * structure validation so that a rejection can only be explained by the
 * invariant rule itself — these exact shapes are reused as live/e2e inputs.
 */

// WHY char-composition instead of filter: filtering generic strings on a
// restricted alphabet rejects most candidates and slows generation badly.
const seedArb: fc.Arbitrary<string> = fc
  .array(fc.constantFrom(...'abcdefghijklmnopqrstuvwxyz0123456789-'.split('')), { minLength: 3, maxLength: 12 })
  .map((chars) => chars.join(''));

/** I-01: skipped/quarantined outcome smuggled in with a positive verdict. */
export const skippedPositiveEvidenceAttempt = fc.record({
  seed: seedArb,
  outcome: fc.constantFrom('skipped', 'quarantined', 'filtered'),
  verdict: fc.constant('pass'),
});

/** I-02: two materials differing in exactly one field but claiming one key. */
export const inputsHashCollisionAttempt = fc
  .record({ seed: seedArb, field: fc.constantFrom('baseSha', 'toolchain', 'lockfiles', 'flags') })
  .map(({ seed, field }) => {
    const base = {
      baseSha: `base-${seed}`,
      lockfiles: [`pnpm-lock-${seed}.yaml`],
      flags: ['--race'],
      toolchain: `go1.23-${seed}`,
    };
    const mutated = { ...base, lockfiles: [...base.lockfiles], flags: [...base.flags] };
    if (field === 'baseSha') mutated.baseSha = `base-${seed}-next`;
    if (field === 'toolchain') mutated.toolchain = `go1.24-${seed}`;
    if (field === 'lockfiles') mutated.lockfiles = [`package-lock-${seed}.json`];
    if (field === 'flags') mutated.flags = ['--race', '-count=1'];
    return { claimedSharedKey: true, first: base, second: mutated };
  });

/** I-03: second evidence record reusing an already-accepted identity. */
export const duplicateAcceptanceAttempt = fc.record({ seed: seedArb }).map(({ seed }) => ({
  first: { runId: prefixedId('run', seed), attempt: 1, leaseJti: prefixedId('lease', seed) },
  second: { runId: prefixedId('run', seed), attempt: 1, leaseJti: prefixedId('lease', seed) },
}));

/** I-04/I-11: completion attempts carrying stale/foreign fence tokens. */
export const staleFenceAttempt = fc
  .record({ seed: seedArb, currentFence: fc.integer({ min: 2, max: 99_999 }) })
  .map(({ seed, currentFence }) => ({
    runId: prefixedId('run', seed),
    currentFence,
    presentedFence: currentFence - 1,
  }));

/** I-14: envelope whose tenant_id differs from the querying tenant. */
export const crossTenantEnvelopeAttempt = fc.record({ seed: seedArb }).map(({ seed }) => ({
  attackerTenant: OTHER_TENANT,
  ownerTenant: TEST_TENANT,
  subjectId: payloads.id('cand', seed),
}));

/** I-07: tamper one field of one event inside an otherwise valid chain. */

export interface ChainTamperCase {
  readonly original: ReadonlyArray<EnvelopeLike>;
  readonly tampered: ReadonlyArray<EnvelopeLike>;
  readonly tamperedSeq: number;
  readonly field: TamperField;
}

const TAMPER_FIELDS = [
  'payload_sha256', 'entry_hash', 'prev_hash', 'seq', 'type', 'version',
] as const;
export type TamperField = (typeof TAMPER_FIELDS)[number];

function mutateField(ev: Record<string, unknown>, field: TamperField): void {
  switch (field) {
    case 'payload_sha256':
      ev.payload_sha256 = 'sha256:' + 'f'.repeat(64);
      break;
    case 'entry_hash':
      ev.entry_hash = 'sha256:' + 'a'.repeat(64);
      break;
    case 'prev_hash':
      // WHY all-'e': genesis is all-'0', so this can never be a no-op even
      // on the first event of a chain.
      ev.prev_hash = 'sha256:' + 'e'.repeat(64);
      break;
    case 'seq':
      ev.seq = (ev.seq as number) + 100;
      break;
    case 'type':
      ev.type = `${ev.type}.forged`;
      break;
    case 'version':
      ev.version = 2;
      break;
  }
}

export const chainTamperCaseArb: fc.Arbitrary<ChainTamperCase> = fc
  .record({
    seeds: fc.array(seedArb, { minLength: 4, maxLength: 4 }),
    index: fc.integer({ min: 0, max: 3 }),
    field: fc.constantFrom(...TAMPER_FIELDS),
  })
  .map(({ seeds, index, field }): ChainTamperCase => {
    const original = buildChainedEnvelopes(seeds.map(chainSpecForSeed));
    // Round-trip through JSON so the tampered copy shares no mutable state.
    const tampered = structuredClone(original);
    const victim = tampered[index] as EnvelopeLike | undefined;
    if (victim) mutateField(victim as unknown as Record<string, unknown>, field);
    return { original, tampered, tamperedSeq: index + 1, field };
  });

function chainSpecForSeed(seed: string, i: number): Parameters<typeof buildChainedEnvelopes>[0][number] {
  const kinds: ReadonlyArray<{
    readonly type: string;
    readonly agg: 'intent' | 'candidate' | 'validation_plan' | 'evidence';
    readonly idPrefix: 'int' | 'cand' | 'val' | 'ev';
    readonly payload: (s: string) => Record<string, unknown>;
  }> = [
    { type: 'intent.declared', agg: 'intent', idPrefix: 'int', payload: payloads.intentDeclared },
    { type: 'candidate.submitted', agg: 'candidate', idPrefix: 'cand', payload: payloads.candidateSubmitted },
    { type: 'validation.planned', agg: 'validation_plan', idPrefix: 'val', payload: payloads.validationPlanned },
    { type: 'evidence.recorded', agg: 'evidence', idPrefix: 'ev', payload: (s) => payloads.evidenceRecorded(s) },
  ];
  // kinds.length is a compile-time constant 4, so the modulo index is always valid.
  const kind = kinds[i % kinds.length] as (typeof kinds)[number];
  return {
    seq: i + 1,
    type: kind.type,
    aggregate: { type: kind.agg, id: payloads.id(kind.idPrefix, seed) },
    payload: kind.payload(seed),
  };
}

export { GENESIS_PREV };
