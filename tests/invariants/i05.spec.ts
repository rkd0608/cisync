import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { checkPathConfinement } from './lib/glob.js';
import { envelopeForType } from './lib/event-schema.js';
import * as payloads from './lib/payloads.js';
import { buildChainedEnvelopes, firstEnvelope } from './lib/builders.js';
import { liveModeEnabled } from './lib/env.js';

/**
 * I-05 — Repair patches confined to contract allowed_paths, enforced
 * SERVER-side pre-accept.
 * Contract mode: schema bounds repair envelopes (globs required, iterations
 * ≤5); a property-tested matcher encodes the confinement rule and flags
 * out-of-contract patch attempts. Live enforcement needs the W2 repair
 * submission API — recorded as pending in docs/EDGE_CASES.md (EC-020).
 */

const pathArb = fc.oneof(
  fc.constant('services/checkout/cart.go'),
  fc.constant('services/checkout/internal/repo/pg.go'),
  fc.constant('cmd/control-plane/main.go'),
  fc.constant('../../etc/passwd'),
  fc.constant('/absolute/escape.go'),
  fc.array(fc.constantFrom(...'abcdefXYZ0123456789._-/'.split('')), { minLength: 1, maxLength: 30 }).map((c) => c.join('')),
);

describe('I-05 contract: repair envelope is glob-bounded by schema', () => {
  it('repair.authorized without allowed_paths is rejected (unbounded repair impossible)', () => {
    const envelopePayload: Record<string, unknown> = {
      repair_id: payloads.id('repair', 'i05'),
      fc_id: payloads.id('fc', 'i05'),
      candidate_id: payloads.id('cand', 'i05'),
      envelope: {
        reproduction_command: 'go test ./...',
        // allowed_paths intentionally absent
        max_iterations: 2,
      },
    };
    const env = firstEnvelope(buildChainedEnvelopes([
      { seq: 1, type: 'repair.authorized', aggregate: { type: 'repair_task', id: payloads.id('repair', 'i05') }, payload: envelopePayload },
]));
    expect(envelopeForType('repair.authorized', env).valid).toBe(false);
  });

  it('max_iterations outside 1..5 is rejected (unbounded repair loops impossible)', () => {
    fc.assert(
      fc.property(fc.integer({ min: 6, max: 100 }), (tooMany) => {
        const payload: Record<string, unknown> = {
          repair_id: payloads.id('repair', 'i05-iters'),
          fc_id: payloads.id('fc', 'i05-iters'),
          candidate_id: payloads.id('cand', 'i05-iters'),
          envelope: { reproduction_command: 'go test ./...', allowed_paths: ['services/**'], max_iterations: tooMany },
        };
        const env = firstEnvelope(buildChainedEnvelopes([
          { seq: 1, type: 'repair.authorized', aggregate: { type: 'repair_task', id: payloads.id('repair', 'i05-iters') }, payload },
]));
        expect(envelopeForType('repair.authorized', env).valid).toBe(false);
      }),
    );
  });
});

describe('I-05 contract: path confinement gate rejects every escape shape', () => {
  it('paths inside the contract pass; everything else fails closed', () => {
    fc.assert(
      fc.property(pathArb, (path) => {
        const verdict = checkPathConfinement(path, ['services/checkout/**'], ['**/*_test.go']);
        if (path.includes('..') || path.startsWith('/')) {
          expect(verdict.allowed).toBe(false);
          expect(verdict.reason).toBe('path_traversal_escape');
          return;
        }
        const insideContract = path === 'services/checkout/cart.go' || path === 'services/checkout/internal/repo/pg.go';
        expect(verdict.allowed).toBe(insideContract && !path.endsWith('_test.go'));
      }),
    );
  });

  it('prohibited globs win even inside allowed surfaces (defense in depth)', () => {
    const verdict = checkPathConfinement('services/checkout/cart_test.go', ['services/checkout/**'], ['services/checkout/**/*_test.go']);
    expect(verdict).toMatchObject({ allowed: false, reason: 'prohibited_path' });
  });

  it('* never crosses directory separators while ** does', () => {
    expect(checkPathConfinement('services/other/file.go', ['services/*/cart.go']).allowed).toBe(false);
    expect(checkPathConfinement('services/x/y/deep/cart.go', ['services/**/cart.go']).allowed).toBe(true);
  });
});
