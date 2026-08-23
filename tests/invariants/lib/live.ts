import {
  candidateAcceptedSchema,
  dossierSchema,
  errorEnvelopeSchema,
  eventsTailSchema,
  fleetClaimResponseSchema,
  intentGrantSchema,
  type CandidateAccepted,
  type Dossier,
  type ErrorEnvelope,
  type EventsTail,
  type FleetClaimResponse,
  type IntentGrant,
  type LedgerEvent,
} from './api-schemas.js';
import { harnessEnv } from './env.js';
import { authHeaders, newIdempotencyKey, request, requestLoose } from './http.js';

/**
 * Live-mode probes against a RUNNING stack. Only reachable when suites are
 * inside describe.skipIf(!liveModeEnabled()); every response is zod-parsed.
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

export async function createIntent(seed: string, repository = 'acme/payments'): Promise<IntentGrant> {
  const res = await request(
    {
      url: `${apiBase()}/change-intents`,
      method: 'POST',
      headers: { ...authedHeaders(), 'Idempotency-Key': newIdempotencyKey(`intent-${seed}`) },
      body: {
        goal: `synthetic validation goal ${seed}`,
        repository,
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

export async function tailEvents(afterSeq = 0, types?: string[]): Promise<EventsTail> {
  const params = new URLSearchParams({ after_seq: String(afterSeq), limit: '500' });
  if (types?.length) params.set('types', types.join(','));
  const res = await request({ url: `${apiBase()}/events?${params}`, method: 'GET', headers: authedHeaders() }, eventsTailSchema);
  if (!res.ok) throw new Error(`tailEvents failed: ${JSON.stringify(res.body)}`);
  return res.body;
}

/** Poll the ledger tail until a matching event appears or the deadline hits. */
export async function waitForEvent(
  matches: (ev: LedgerEvent) => boolean,
  opts: { timeoutMs?: number; description: string },
): Promise<LedgerEvent> {
  const deadline = Date.now() + (opts.timeoutMs ?? 15_000);
  let cursor = 0;
  let seen = 0;
  while (Date.now() < deadline) {
    const page = await tailEvents(cursor);
    for (const ev of page.events) {
      if (matches(ev)) return ev;
    }
    seen = Math.max(seen, page.next_seq);
    cursor = page.next_seq;
    if (page.events.length === 0) await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`waitForEvent timed out (${opts.description}); ledger head at seq ${seen}`);
}

export interface ClaimedJob {
  job: FleetClaimResponse['jobs'][number];
}

export async function claimFleetJob(): Promise<ClaimedJob | undefined> {
  const res = await requestLoose({
    url: `${fleetBase()}/internal/fleet/jobs/claim`,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: { pool: 'sim', limit: 4 },
  });
  if (res.status !== 200) throw new Error(`fleet claim returned ${res.status}: ${String(res.rawText).slice(0, 200)}`);
  const parsed = fleetClaimResponseSchema.parse(res.body);
  const job = parsed.jobs[0];
  return job ? { job } : undefined;
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
): Promise<FleetCompleteOutcome> {
  const res = await requestLoose({
    url: `${fleetBase()}/internal/fleet/jobs/${runId}/complete`,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
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
