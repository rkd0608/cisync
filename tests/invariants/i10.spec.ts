import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { liveModeEnabled } from './lib/env.js';
import { apiBase, authedHeaders, expectErrorBody } from './lib/live.js';
import { errorEnvelopeSchema, intentGrantSchema } from './lib/api-schemas.js';
import { HttpError, newIdempotencyKey, request } from './lib/http.js';

/**
 * I-10 — Admission control precedes queueing: budgets/WIP caps are
 * atomically reserved; overflow ⇒ 429 `budget_exceeded`, never overrun.
 * Contract mode: the admission-refusal envelope contract (machine-readable,
 * agent-regulable) is property-checked.
 * Live mode: a concurrent burst produces ONLY grants or typed 429 signals
 * — never a silent queue-overrun or 5xx.
 */

describe('I-10 contract: refusal envelope is machine-readable', () => {
  const admissionEnvelope = errorEnvelopeSchema;

  it('budget_exceeded refusals parse with code + message; retry hints are positive ints', () => {
    fc.assert(
      fc.property(
        fc.constantFrom('budget_exceeded', 'rate_limited'),
        fc.option(fc.integer({ min: 1, max: 3600 }), { nil: undefined }),
        fc.string({ minLength: 1, maxLength: 40 }),
        (code, retryAfter, message) => {
          const body = {
            error: { code, message, ...(retryAfter === undefined ? {} : { retry_after_s: retryAfter }) },
          };
          // WHY parse-not-assert-shape: services may add fields; the
          // contract only fixes these semantics.
          const parsed = admissionEnvelope.safeParse(body);
          expect(parsed.success).toBe(true);
          if (parsed.success && parsed.data.error.retry_after_s != null) {
            expect(parsed.data.error.retry_after_s).toBeGreaterThan(0);
          }
        },
      ),
      { numRuns: 100 },
    );
  });

  it('an untyped refusal (no error.code) violates the contract', () => {
    expect(admissionEnvelope.safeParse({ error: { message: 'busy' } }).success).toBe(false);
    expect(admissionEnvelope.safeParse({ rejected: true }).success).toBe(false);
  });
});

describe.skipIf(!liveModeEnabled())('I-10 live: burst sees only grants or typed refusals', () => {
  it('concurrent intent burst never overruns admission (5xx-free)', async () => {
    const attempts = Array.from({ length: 16 }, (_, i) =>
      request(
        {
          url: `${apiBase()}/change-intents`,
          method: 'POST',
          headers: { ...authedHeaders(), 'Idempotency-Key': newIdempotencyKey(`i10-burst-${i}`) },
          body: {
            goal: `admission burst ${i}`,
            repository: `acme/adm-${i % 4}`,
            base: 'main',
            expected_surfaces: ['services/**'],
            acceptance_criteria: ['burst'],
            risk: 'low',
          },
        },
        intentGrantSchema,
      ),
    );
    const settled = await Promise.allSettled(attempts);
    for (const [i, outcome] of settled.entries()) {
      if (outcome.status === 'fulfilled') {
        expect([200], `unexpected status on grant ${i}`).toContain(outcome.value.status);
        continue;
      }
      if (!(outcome.reason instanceof HttpError)) throw outcome.reason;
      expect(outcome.reason.status).toBeLessThan(500);
      const envelope = await expectErrorBody(outcome.reason.status, outcome.reason.rawBody);
      if (outcome.reason.status === 429) {
        expect(['budget_exceeded', 'rate_limited']).toContain(envelope.error.code);
      }
    }
  });
});
