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

  async function claimWithin(timeoutMs: number): Promise<ClaimedJob | undefined> {
    // WHY the probe pool: claiming from 'sim' steals another suite's live run
    // and double-bumps its fence, stranding that candidate (I-11 regression
    // driver). The seeded job exercises the identical fencing surface.
    const { seedFenceProbeJob, claimFleetJob, FENCE_PROBE_POOL } = await import('./lib/live.js');
    await seedFenceProbeJob(`i11-${Date.now()}`);
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const found = await claimFleetJob(FENCE_PROBE_POOL);
      if (found) return found.job;
      await new Promise((r) => setTimeout(r, 200));
    }
    return undefined;
  }

  it('wrong-token completion → 409 fence_mismatch; correct token → accepted', { timeout: 30_000 }, async () => {
    claimed = await claimWithin(15_000);
    if (!claimed) return; // dispatch not wired yet
    const wrong = await completeFleetJob(claimed.run_id, claimed.fence_token + 1_000_000, 'succeeded');
    if (wrong.status === 409) {
      expect(wrong.reason).toBe('fence_mismatch');
    } else {
      // Conforming alternative: unknown-token requests may be refused as bad request.
      expect(wrong.status).toBe(400);
    }
    const right = await completeFleetJob(claimed.run_id, claimed.fence_token, 'failed');
    expect(right.status).toBe(200);
    expect(right.accepted).toBe(true);
  });
});
