import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { envelopeForType } from './lib/event-schema.js';
import * as payloads from './lib/payloads.js';
import { buildChainedEnvelopes, firstEnvelope } from './lib/builders.js';
import { liveModeEnabled } from './lib/env.js';
import { authedHeaders, apiBase } from './lib/live.js';
import { intentGrantSchema } from './lib/api-schemas.js';
import { HttpError, newIdempotencyKey, request } from './lib/http.js';

/**
 * I-06 — Budget conservation: Σ reservations − Σ releases = current usage;
 * crash-safe via ledger events.
 * Contract mode: released_budget can never exceed the granted budget and
 * every release carries a machine-readable reason.
 * Live mode: overflow is signalled with 429 budget_exceeded (never silent
 * overrun), and releasing a lease returns capacity — exact conservation
 * accounting is asserted by the storm (W3) over full ledger replays.
 */

interface LeaseState {
  granted: { cpu_minutes: number; environment_minutes: number; repair_attempts: number };
  released: { cpu_minutes: number; environment_minutes: number; repair_attempts: number };
}

/** Conservation predicate over replayed lease lifecycle events. */
export function conservesBudget(states: Map<string, LeaseState>): boolean {
  for (const s of states.values()) {
    const outstanding = {
      cpu: s.granted.cpu_minutes - s.released.cpu_minutes,
      env: s.granted.environment_minutes - s.released.environment_minutes,
      repair: s.granted.repair_attempts - s.released.repair_attempts,
    };
    if (outstanding.cpu < 0 || outstanding.env < 0 || outstanding.repair < 0) return false;
  }
  return true;
}

const budgetArb = fc.record({
  cpu_minutes: fc.integer({ min: 0, max: 500 }),
  environment_minutes: fc.integer({ min: 0, max: 500 }),
  repair_attempts: fc.integer({ min: 0, max: 5 }),
});

describe('I-06 contract: releases never exceed grants; reasons are frozen', () => {
  it('released_budget above the grant is rejected by conservation predicate', () => {
    fc.assert(
      fc.property(budgetArb, (grant) => {
        const states = new Map([['lease_x', { granted: grant, released: { cpu_minutes: grant.cpu_minutes + 1, environment_minutes: grant.environment_minutes, repair_attempts: grant.repair_attempts } }]]);
        expect(conservesBudget(states)).toBe(false);
      }),
    );
  });

  it('equal-or-smaller releases conserve exactly', () => {
    fc.assert(
      fc.property(budgetArb, budgetArb, (grant, release) => {
        const capped = {
          cpu_minutes: Math.min(release.cpu_minutes, grant.cpu_minutes),
          environment_minutes: Math.min(release.environment_minutes, grant.environment_minutes),
          repair_attempts: Math.min(release.repair_attempts, grant.repair_attempts),
        };
        expect(conservesBudget(new Map([['l', { granted: grant, released: capped }]]))).toBe(true);
      }),
    );
  });

  it('lease.revoked reason enum rejects unknown causes', () => {
    const forged = structuredClone(payloads.leaseGranted('i06'));
    const payload: Record<string, unknown> = {
      lease_id: forged['lease_id'],
      reason: 'operator_whim',
      released_budget: payloads.budget('i06'),
    };
    const env = firstEnvelope(buildChainedEnvelopes([
      { seq: 1, type: 'lease.revoked', aggregate: { type: 'lease', id: String(forged['lease_id']) }, payload },
]));
    expect(envelopeForType('lease.revoked', env).valid).toBe(false);
  });
});

describe.skipIf(!liveModeEnabled())('I-06 live: overflow is refused with a machine-readable budget signal', () => {
  // WHY 60s: the contract here is the OUTCOME distribution (2xx or typed
  // 429), not wall-clock; under full-suite concurrency the burst may queue.
  it('concurrent burst yields only successes or typed rejections — never overrun or 5xx', { timeout: 60_000 }, async () => {
    const outcomes: string[] = [];
    const results = await Promise.allSettled(
      Array.from({ length: 24 }, (_, i) =>
        request(
          {
            url: `${apiBase()}/change-intents`,
            method: 'POST',
            headers: { ...authedHeaders(), 'Idempotency-Key': newIdempotencyKey(`i06-burst-${i}`) },
            body: {
              goal: `budget burst ${i}`, repository: `acme/burst-${i % 3}`, base: 'main',
              expected_surfaces: ['services/**'], acceptance_criteria: ['x'], risk: 'low',
            },
          },
          intentGrantSchema,
        ),
      ),
    );
    for (const r of results) {
      if (r.status === 'fulfilled') {
        expect(r.value.status).toBe(200);
        // WHY the raw status, not a label: the acceptance predicate below
        // classifies on it; 'granted' strings masked non-2xx successes and
        // made every-fulfilled runs look like violations.
        outcomes.push(String(r.value.status));
        continue;
      }
      if (r.reason instanceof HttpError) {
        const envelope = await import('./lib/live.js').then((m) => m.expectErrorBody(r.reason.status, r.reason.rawBody));
        expect(envelope.error.code).toBeTypeOf('string');
        outcomes.push(`${r.reason.status}:${envelope.error.code}`);
      } else {
        throw r.reason;
      }
    }
    // Every refusal is one of the two documented admission signals.
    console.log('I06-OUTCOMES', JSON.stringify(outcomes));
    for (const outcome of outcomes) {
      if (outcome.startsWith('429')) {
        expect(['429:budget_exceeded', '429:rate_limited']).toContain(outcome);
      } else {
        expect(outcome.startsWith('2')).toBe(true);
      }
    }
  });
});
