import {
  candidateAcceptedSchema,
  dossierSchema,
  errorEnvelopeSchema,
  fleetClaimResponseSchema,
  intentGrantSchema,
  type CandidateAccepted,
  type Dossier,
  type ErrorEnvelope,
  type FleetClaimResponse,
  type IntentGrant,
  type LedgerEvent,
} from './api-schemas.js';
import { harnessEnv } from './env.js';
import { mintJobLeaseToken } from './joblease.js';
import { authHeaders, newIdempotencyKey, request, requestLoose } from './http.js';
import * as tail from './tail.js';

/**
 * Live-mode probes against a RUNNING stack. Only reachable when suites are
 * inside describe.skipIf(!liveModeEnabled()); every response is zod-parsed.
 * Ledger-tail readers live in ./tail.js; the helpers here bind them to this
 * module's resolved API base.
 */

function requireApiUrl(): string {
  const url = harnessEnv().apiUrl;
  if (!url) throw new Error('live probe invoked without SAURON_API_URL');
  return url.replace(/\/$/, '');
}

export function apiBase(): string {
  return `${requireApiUrl()}/v1`;
}

export function fleetBase(): string {
  return harnessEnv().fleetUrl.replace(/\/$/, '');
}

export function ingestBase(): string {
  return harnessEnv().ingestUrl.replace(/\/$/, '');
}

export function authedHeaders(): Record<string, string> {
  return authHeaders(harnessEnv().adminToken);
}

/**
 * Run-scoped token so repeated vitest runs against one persistent dev DB
 * never collide. WHY: clustering parks a duplicate_of candidate as
 * blocked_representative at submission and clusters never leave 'active',
 * so a repo that already holds a resolved representative would permanently
 * block every future probe submitted into it.
 */
const runToken = Date.now().toString(36);

export async function createIntent(seed: string, repository?: string): Promise<IntentGrant> {
  const res = await request(
    {
      url: `${apiBase()}/change-intents`,
      method: 'POST',
      headers: { ...authedHeaders(), 'Idempotency-Key': newIdempotencyKey(`intent-${seed}`) },
      body: {
        goal: `synthetic validation goal ${seed}`,
        // Seed-scoped within a run so probes stay independent; the supersede
        // journey still exercises clustering deliberately (e2e).
        repository: repository ?? `acme/inv-${runToken}-${seed}`,
        base: 'main',
        expected_surfaces: ['services/checkout/**'],
        acceptance_criteria: [`criterion-${seed}`],
        risk: 'medium',
      },
    },
    intentGrantSchema,
  );
  if (!res.ok) throw new Error(`createIntent failed: ${JSON.stringify(res.body)}`);
  return res.body;
}

export async function submitCandidate(
  intentId: string,
  seed: string,
  overrides: Partial<{ headSha: string; baseSha: string }> = {},
): Promise<CandidateAccepted> {
  const sha40 = (s: string): string => Buffer.from(s.repeat(8), 'utf8').subarray(0, 20).toString('hex');
  const res = await request(
    {
      url: `${apiBase()}/change-intents/${intentId}/candidates`,
      method: 'POST',
      headers: { ...authedHeaders(), 'Idempotency-Key': newIdempotencyKey(`cand-${seed}`) },
      body: {
        patch_ref: `bundle:test-${seed}`,
        head_sha: overrides.headSha ?? sha40(`head-${seed}`),
        base_sha: overrides.baseSha ?? sha40(`base-${seed}`),
        changed_paths: ['services/checkout/cart.go'],
      },
    },
    candidateAcceptedSchema,
  );
  if (!res.ok) throw new Error(`submitCandidate failed: ${JSON.stringify(res.body)}`);
  return res.body;
}

export async function getDossier(candidateId: string): Promise<Dossier> {
  const res = await request({ url: `${apiBase()}/candidates/${candidateId}/dossier`, method: 'GET', headers: authedHeaders() }, dossierSchema);
  if (!res.ok) throw new Error(`getDossier failed: ${JSON.stringify(res.body)}`);
  return res.body;
}

/** Recent-history read (bounded lookback from head) for one-shot probes. */
export function drainEvents(lookback?: number): Promise<LedgerEvent[]> {
  return tail.recentEvents(apiBase(), lookback);
}

/** Incremental matcher poll over the tail; [] on timeout. */
export function findEvents(
  matches: (ev: LedgerEvent) => boolean,
  opts: { timeoutMs?: number; pollMs?: number },
): Promise<LedgerEvent[]> {
  return tail.findEvents(apiBase(), matches, opts);
}

/** Poll the ledger tail until a matching event appears or the deadline hits. */
export async function waitForEvent(
  matches: (ev: LedgerEvent) => boolean,
  opts: { timeoutMs?: number; description: string },
): Promise<LedgerEvent> {
  const deadline = Date.now() + (opts.timeoutMs ?? 15_000);
  let cursor = await tail.recentStart(apiBase());
  let seen = 0;
  while (Date.now() < deadline) {
    const scan = await tail.scanTail(apiBase(), cursor);
    for (const ev of scan.events) {
      if (matches(ev)) return ev;
    }
    seen = Math.max(seen, scan.cursor);
    cursor = scan.cursor;
    if (scan.caughtUp) await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`waitForEvent timed out (${opts.description}); ledger head at seq ${seen}`);
}

export interface ClaimedJob {
  job: FleetClaimResponse['jobs'][number];
}

// Fleet-side helpers moved to live-fleet.ts (file-cap split); re-exported so
// suite imports stay stable.
export {
  FENCE_PROBE_POOL,
  seedFenceProbeJob,
  claimFleetJob,
  completeFleetJob,
  expectErrorBody,
} from './live-fleet';
