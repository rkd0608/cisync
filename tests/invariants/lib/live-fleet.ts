/**
 * Fleet-side live-test helpers: authenticated probe-job seeding, claim and
 * fenced completion. Split from live.ts for the 250-line file cap.
 */
import { requestLoose } from './http';
import {
  errorEnvelopeSchema,
  fleetClaimResponseSchema,
  type ErrorEnvelope,
} from './api-schemas.js';
import { fleetBase } from './live';
import { mintJobLeaseToken } from './joblease.js';
import type { ClaimedJob } from './live';

/**
 * Prefix for pools used ONLY by fence/complete probes that seed their own
 * synthetic jobs. Each seeded job gets its own suffixed pool: claiming from a
 * SHARED probe pool hands back the OLDEST queued row, so leftovers from
 * earlier suite runs (or a concurrently running suite's target) arrive
 * without a matching credential and starve every later probe behind the
 * LIMIT-4 head (live W4 finding). Private pools make claims self-only.
 */
export const FENCE_PROBE_POOL = 'test-fence-probe';

let probeCounter = 0;

/**
 * Seed one synthetic job into a FRESH private probe pool and return its
 * identity plus the dispatch-time credential bound to it (fence 1, the epoch
 * the first claim produces). run_id is a valid prefixed ULID so served
 * envelopes stay schema-clean.
 */
export interface SeededProbeJob {
  runId: string;
  pool: string;
  leaseToken: string;
}

export async function seedFenceProbeJob(tag: string): Promise<SeededProbeJob> {
  probeCounter += 1;
  const alphabet = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
  let ulid = '';
  for (let i = 0; i < 26; i++) {
    ulid += alphabet[Math.floor(Math.random() * alphabet.length)];
  }
  const runId = `run_${ulid}`;
  const pool = `${FENCE_PROBE_POOL}-${probeCounter}-${Math.floor(Math.random() * 1e9).toString(36)}`;
  // P0-1/B2: the probe presents a REAL credential — without it every
  // mutation on the job is refused 401 unauthorized by the fleet gate.
  const leaseToken = mintJobLeaseToken({ run_id: runId, attempt: 1, fence_token: 1, repo: `acme/fence-probe-${tag}` });
  if (!leaseToken) throw new Error('dev job-lease key missing (run `make keys`); cannot seed authenticated probe jobs');
  const res = await requestLoose({
    url: `${fleetBase()}/internal/fleet/jobs`,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: {
      run_id: runId,
      attempt: 1,
      tier: 1,
      pool,
      lease_token: leaseToken,
      job_spec: {
        kind: 'selected_unit',
        repo: `acme/fence-probe-${tag}`,
        base_sha: sha40(`fprobe-base-${tag}`),
        head_sha: sha40(`fprobe-head-${tag}`),
        patch_ref: `bundle:fprobe-${tag}`,
        inputs_hash: 'sha256:' + '0'.repeat(64),
        timeout_ms: 60000,
      },
    },
  });
  if (res.status !== 200 && res.status !== 201 && res.status !== 202) {
    throw new Error(`seed job returned ${res.status}: ${String(res.rawText).slice(0, 200)}`);
  }
  return { runId, pool, leaseToken };
}

/**
 * Claim the SEEDED job from its private pool. Production runners present the
 * claim-returned lease_token on every mutation; the seed mints the identical
 * credential itself (same run/attempt/fence binding), so callers pass
 * `seed.leaseToken` back on heartbeat/complete.
 */
export async function claimFleetJob(pool = 'sim'): Promise<ClaimedJob | undefined> {
  const res = await requestLoose({
    url: `${fleetBase()}/internal/fleet/jobs/claim`,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: { pool, limit: 4 },
  });
  if (res.status !== 200) throw new Error(`fleet claim returned ${res.status}: ${String(res.rawText).slice(0, 200)}`);
  const parsed = fleetClaimResponseSchema.parse(res.body);
  const job = parsed.jobs[0];
  return job ? { job } : undefined;
}

/** Bearer header for one stored probe credential. */
function leaseHeaders(leaseToken?: string): Record<string, string> {
  if (!leaseToken) throw new Error('probe completion requires the claim-returned lease_token');
  return { Authorization: `Bearer ${leaseToken}` };
}

function sha40(seed: string): string {
  return Buffer.from(seed.repeat(8), 'utf8').subarray(0, 20).toString('hex');
}

export interface FleetCompleteOutcome {
  status: number;
  accepted?: boolean;
  reason?: string;
  body: unknown;
}

export async function completeFleetJob(
  runId: string,
  fenceToken: number,
  status: 'succeeded' | 'failed' | 'timed_out',
  leaseToken?: string,
): Promise<FleetCompleteOutcome> {
  const res = await requestLoose({
    url: `${fleetBase()}/internal/fleet/jobs/${runId}/complete`,
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...leaseHeaders(leaseToken) },
    body: {
      fence_token: fenceToken,
      status,
      logs_digest: 'sha256:' + 'b'.repeat(64),
      artifact_digests: [],
      duration_ms: 900,
      actual_cost_millicents: 42,
    },
  });
  const body = res.body as { accepted?: boolean; reason?: string } | null;
  return { status: res.status, accepted: body?.accepted, reason: body?.reason, body: res.body };
}

export async function expectErrorBody(status: number, rawText: string): Promise<ErrorEnvelope> {
  const parsed = errorEnvelopeSchema.safeParse(JSON.parse(rawText || '{}'));
  if (!parsed.success) throw new Error(`${status} response was not an ErrorEnvelope: ${rawText.slice(0, 300)}`);
  return parsed.data;
}
