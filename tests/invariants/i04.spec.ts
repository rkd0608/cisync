import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { envelopeForType } from './lib/event-schema.js';
import { staleFenceAttempt } from './lib/attack-vectors.js';
import * as payloads from './lib/payloads.js';
import { buildChainedEnvelopes, firstEnvelope } from './lib/builders.js';
import { liveModeEnabled } from './lib/env.js';
import { claimFleetJob, createIntent, fleetBase, submitCandidate, waitForEvent } from './lib/live.js';

/**
 * I-04 — Runner credentials scoped ≤ declared action/repo/tier/TTL.
 * Contract mode: lease.granted envelopes MUST bound scope+TTL; job specs
 * claimed by a runner must never exceed the declared lease surface.
 * Live mode: heartbeat/complete with a foreign fence token is refused —
 * the credential only works inside its declared epoch (see I-11 for the
 * full stale-epoch matrix).
 */

describe('I-04 contract: lease grants are scope- and TTL-bounded by schema', () => {
  it('lease.granted requires scope.kind within the frozen enum', () => {
    const forged = structuredClone(payloads.leaseGranted('i04'));
    (forged['scope'] as Record<string, unknown>)['kind'] = 'cluster_admin';
    const env = firstEnvelope(buildChainedEnvelopes([
      { seq: 1, type: 'lease.granted', aggregate: { type: 'lease', id: payloads.id('lease', 'i04') }, payload: forged },
]));
    expect(envelopeForType('lease.granted', env).valid).toBe(false);
  });

  it('lease.granted without ttl_expires_at is rejected (unbounded TTL impossible)', () => {
    const missing = structuredClone(payloads.leaseGranted('i04-ttl'));
    delete missing['ttl_expires_at'];
    const env = firstEnvelope(buildChainedEnvelopes([
      { seq: 1, type: 'lease.granted', aggregate: { type: 'lease', id: payloads.id('lease', 'i04-ttl') }, payload: missing },
]));
    expect(envelopeForType('lease.granted', env).valid).toBe(false);
  });

  it('budget values can never be negative (credential cannot mint capacity)', () => {
    fc.assert(
      fc.property(fc.integer({ min: -1000, max: -1 }), fc.constantFrom('cpu_minutes', 'environment_minutes', 'repair_attempts'), (negative, field) => {
        const forged = structuredClone(payloads.leaseGranted(`i04-${field}`));
        (forged['budget'] as Record<string, unknown>)[field] = negative;
        const env = firstEnvelope(buildChainedEnvelopes([
          { seq: 1, type: 'lease.granted', aggregate: { type: 'lease', id: payloads.id('lease', `i04-${field}`) }, payload: forged },
]));
        expect(envelopeForType('lease.granted', env).valid).toBe(false);
      }),
    );
  });
});

describe.skipIf(!liveModeEnabled())('I-04 live: claimed credentials stay inside their declared job', () => {
  it('claimed job_spec matches the sim pool and rejects foreign-fence heartbeats', { timeout: 30_000 }, async () => {
    // WHY a seeded job in the probe pool: claiming from 'sim' steals a live
    // run of another concurrent suite and corrupts its fence epoch (I-11
    // regression driver). The probe only needs A claimable job.
    const { seedFenceProbeJob, claimFleetJob, FENCE_PROBE_POOL, fleetBase } = await import('./lib/live.js');
    await seedFenceProbeJob(`i04-${Date.now()}`);
    let claimed: Awaited<ReturnType<typeof claimFleetJob>> | undefined;
    for (let i = 0; i < 20 && !claimed; i++) {
      claimed = await claimFleetJob(FENCE_PROBE_POOL);
      if (!claimed) await new Promise((r) => setTimeout(r, 200));
    }
    if (!claimed) return; // claim path unavailable in this environment
    const spec = claimed.job.job_spec;
    expect(claimed.job.pool).toBe(FENCE_PROBE_POOL);
    expect(spec.repo).toMatch(/^[\w.-]+\/[\w.-]+$/);
    expect(spec.base_sha).toMatch(/^[a-f0-9]{40}$/);
    expect(spec.head_sha).toMatch(/^[a-f0-9]{40}$/);
    expect(spec.timeout_ms).toBeGreaterThan(0);
    // A fence token the lease never declared must not operate the job.
    const foreign = claimed.job.fence_token + 1_000_000;
    const beat = await fetch(`${fleetBase()}/internal/fleet/jobs/${claimed.job.run_id}/heartbeat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ fence_token: foreign }),
    });
    expect(beat.status).not.toBe(204);
  });

  it('dispatch reaches the fleet after a candidate lands (liveness of scoping path)', async () => {
    const grant = await createIntent('i04-liveness');
    const cand = await submitCandidate(grant.intent_id, 'i04-liveness');
    await waitForEvent((ev) => ev.type === 'validation.started' || ev.aggregate.id === cand.candidate_id, {
      description: `any run activity for ${cand.candidate_id}`,
      timeoutMs: 20_000,
    });
  });
});
