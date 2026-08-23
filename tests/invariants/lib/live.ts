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

/**
 * Pool used ONLY by fence/complete probes that seed their own synthetic jobs.
 * WHY a private pool: claiming from 'sim' steals whatever scheduler-dispatched
 * run is at the queue head — the extra claims double-bump its fence epoch, so
 * the real completion can never match control-plane's fence and the victim
 * candidate is starved of evidence (the i01/journey flake class). Jobs seeded
 * into this pool are invisible to the executor and to other suites.
 */
export const FENCE_PROBE_POOL = 'test-fence-probe';

let probeCounter = 0;

/**
 * Seed one synthetic job into the fence-probe pool and return its identity.
 * run_id is a valid prefixed ULID so served envelopes stay schema-clean.
 */
export async function seedFenceProbeJob(tag: string): Promise<{ runId: string; pool: string }> {
  probeCounter += 1;
  const alphabet = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
  let ulid = '';
  for (let i = 0; i < 26; i++) {
    ulid += alphabet[Math.floor(Math.random() * alphabet.length)];
  }
  const runId = `run_${ulid}${probeCounter <= 1 ? '' : ''}`;
  const res = await requestLoose({
    url: `${fleetBase()}/internal/fleet/jobs`,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: {
      run_id: runId,
      attempt: 1,
      tier: 1,
      pool: FENCE_PROBE_POOL,
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
  return { runId, pool: FENCE_PROBE_POOL };
}

function sha40(seed: string): string {
  return Buffer.from(seed.repeat(8), 'utf8').subarray(0, 20).toString('hex');
}

/** Claim ONE job from the given pool (default: the real sim pool). */
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
