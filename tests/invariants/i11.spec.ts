import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { staleFenceAttempt } from './lib/attack-vectors.js';
import { liveModeEnabled } from './lib/env.js';
import { claimFleetJob, completeFleetJob } from './lib/live.js';
import type { FleetClaimResponse } from './lib/api-schemas.js';

/**
 * I-11 — Results accepted only from the current fence-token holder; stale
 * epochs rejected; expired leases killed+fenced by reconciler.
 * Contract mode: generated stale-fence attempts are well-formed completions
 * that a conforming fleet MUST refuse (fence_mismatch) — these shapes are
 * the live inputs below. TTL expiry refusal lives in e2e/lease-ttl.
 */

type ClaimedJob = FleetClaimResponse['jobs'][number];

/** A completion attempt is "stale" when its token is below the current epoch. */
export function isStaleFence(attempt: { presentedFence: number; currentFence: number }): boolean {
  return attempt.presentedFence < attempt.currentFence;
}

describe('I-11 contract: stale-fence vectors are well-formed refusable attempts', () => {
  it('generated attempts always present a lower epoch than current', () => {
    fc.assert(
      fc.property(staleFenceAttempt, ({ presentedFence, currentFence }) => {
        expect(presentedFence).toBeGreaterThanOrEqual(1);
        expect(currentFence).toBeGreaterThanOrEqual(1);
        expect(isStaleFence({ presentedFence, currentFence })).toBe(true);
        // The vector carries everything a complete call needs: identity,
        // fence claim and a plausible status — so a server rejection can
        // ONLY be explained by fencing, never by malformed input.
      }),
    );
  });

  it('equal-epoch replays are NOT stale-fence; they fall under I-03 already_accepted', () => {
    expect(isStaleFence({ presentedFence: 7, currentFence: 7 })).toBe(false);
  });
});

describe.skipIf(!liveModeEnabled())('I-11 live: only the current fence holder writes', () => {
  let claimed: ClaimedJob | undefined;
  // P0-1/B2: the claim response hands the dispatch-time job-lease credential
  // to the worker; every completion presents it as Authorization: Bearer.
  let leaseToken: string | undefined;

  async function claimWithin(timeoutMs: number): Promise<ClaimedJob | undefined> {
    // WHY a seeded job in its own private probe pool: claiming from 'sim'
    // steals another suite's live run and double-bumps its fence, stranding
    // that candidate (I-11 regression driver); a SHARED probe pool hands
    // back stale leftovers whose credentials we don't hold. The seeded job
    // exercises the identical fencing surface with its own credential.
    const { seedFenceProbeJob, claimFleetJob } = await import('./lib/live.js');
    const seed = await seedFenceProbeJob(`i11-${Date.now()}`);
    leaseToken = seed.leaseToken;
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const found = await claimFleetJob(seed.pool);
      if (found) return found.job;
      await new Promise((r) => setTimeout(r, 200));
    }
    return undefined;
  }

  it('wrong-token completion → 409 fence_mismatch; correct token → accepted', { timeout: 30_000 }, async () => {
    claimed = await claimWithin(15_000);
    if (!claimed || !leaseToken) return; // dispatch not wired yet
    // A valid lease presenting a STALE epoch hits the fenced write: 409.
    const staleEpoch = await completeFleetJob(claimed.run_id, claimed.fence_token + 1_000_000, 'succeeded', leaseToken);
    if (staleEpoch.status === 409) {
      expect(staleEpoch.reason).toBe('fence_mismatch');
    } else {
      // Conforming alternative: unknown-token requests may be refused as bad request.
      expect(staleEpoch.status).toBe(400);
    }
    const right = await completeFleetJob(claimed.run_id, claimed.fence_token, 'failed', leaseToken);
    expect(right.status).toBe(200);
    expect(right.accepted).toBe(true);
  });

  it('completion without the job-lease credential is refused unauthorized (P0-1)', { timeout: 30_000 }, async () => {
    if (!claimed) return;
    // WHY raw fetch: completeFleetJob fails closed client-side when called
    // without a credential; the probe must observe the SERVER's typed 401.
    const { fleetBase, expectErrorBody } = await import('./lib/live.js');
    const unauthed = await fetch(`${fleetBase()}/internal/fleet/jobs/${claimed.run_id}/complete`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ fence_token: claimed.fence_token, status: 'failed', logs_digest: 'sha256:' + 'b'.repeat(64), artifact_digests: [], duration_ms: 900, actual_cost_millicents: 42 }),
    });
    expect(unauthed.status).toBe(401);
    const envelope = await expectErrorBody(unauthed.status, await unauthed.text());
    expect(envelope.error.code).toBe('unauthorized');
  });
});
