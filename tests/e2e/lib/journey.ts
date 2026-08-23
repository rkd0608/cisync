import {
  candidateAcceptedSchema,
  errorEnvelopeSchema,
  eventsTailSchema,
  intentGrantSchema,
  leaseRenewalSchema,
  type CandidateAccepted,
  type ErrorEnvelope,
  type EventsTail,
  type IntentGrant,
  type LedgerEvent,
} from '../../invariants/lib/api-schemas.js';
import { harnessEnv } from '../../invariants/lib/env.js';
import { authHeaders, newIdempotencyKey, request } from '../../invariants/lib/http.js';

/**
 * Journey helpers for compose-up black-box suites. Same zod-validated HTTP
 * discipline as invariants/live; separated because journeys compose many
 * steps and need polling utilities.
 */

export function api(): string {
  const url = harnessEnv().apiUrl ?? 'http://localhost:8081';
  return `${url.replace(/\/$/, '')}/v1`;
}

export function ingest(): string {
  return harnessEnv().ingestUrl.replace(/\/$/, '');
}

export function headers(): Record<string, string> {
  return authHeaders(harnessEnv().adminToken);
}

export async function createIntent(seed: string): Promise<IntentGrant> {
  const res = await request(
    {
      url: `${api()}/change-intents`,
      method: 'POST',
      headers: { ...headers(), 'Idempotency-Key': newIdempotencyKey(`e2e-intent-${seed}`) },
      body: {
        goal: `e2e journey ${seed}`,
        repository: `acme/e2e-${seed.split('-')[0] ?? 'x'}`,
        base: 'main',
        expected_surfaces: ['services/checkout/**'],
        acceptance_criteria: [`ac-${seed}`],
        risk: 'medium',
      },
    },
    intentGrantSchema,
  );
  if (!res.ok) throw new Error(`createIntent ${seed}: ${JSON.stringify(res.body)}`);
  return res.body;
}

const sha40 = (s: string): string => Buffer.from(s.repeat(4), 'utf8').subarray(0, 20).toString('hex');

export async function submitCandidate(intentId: string, seed: string, changedPaths?: string[]): Promise<CandidateAccepted> {
  const res = await request(
    {
      url: `${api()}/change-intents/${intentId}/candidates`,
      method: 'POST',
      headers: { ...headers(), 'Idempotency-Key': newIdempotencyKey(`e2e-cand-${seed}`) },
      body: {
        patch_ref: `bundle:e2e-${seed}`,
        head_sha: sha40(`head-${seed}`),
        base_sha: sha40(`base-${seed}`),
        ...(changedPaths ? { changed_paths: changedPaths } : {}),
      },
    },
    candidateAcceptedSchema,
  );
  if (!res.ok) throw new Error(`submitCandidate ${seed}: ${JSON.stringify(res.body)}`);
  return res.body;
}

/** Fetch one page of the tail; caller loops via waitFor. */
export async function tail(afterSeq = 0): Promise<EventsTail> {
  const res = await request({ url: `${api()}/events?after_seq=${afterSeq}&limit=500`, method: 'GET', headers: headers() }, eventsTailSchema);
  if (!res.ok) throw new Error(`tail: ${JSON.stringify(res.body)}`);
  return res.body;
}

/** Poll the whole tail until the predicate matches; throws with context. */
export async function waitForLedger(
  description: string,
  matches: (events: readonly LedgerEvent[]) => boolean,
  timeoutMs = 90_000,
): Promise<LedgerEvent[]> {
  const deadline = Date.now() + timeoutMs;
  let snapshot: LedgerEvent[] = [];
  while (Date.now() < deadline) {
    snapshot = await drainTail();
    if (matches(snapshot)) return snapshot;
    await new Promise((r) => setTimeout(r, 500));
  }
  const types = snapshot.map((e) => e.type);
  throw new Error(`timeout waiting for ${description}; saw [${[...new Set(types)].join(', ')}]`);
}

/** Drain the whole tail once (exported for negative assertions). */
export async function drainTailFor(
  matches: (events: readonly LedgerEvent[]) => boolean,
  description: string,
  timeoutMs = 90_000,
): Promise<LedgerEvent[]> {
  return waitForLedger(description, matches, timeoutMs);
}

/** Drain the recent tail once (bounded lookback; candidate events are fresh). */
async function drainTail(): Promise<LedgerEvent[]> {
  const { apiBase } = await import('../../invariants/lib/live.js');
  const { recentStart, scanTail } = await import('../../invariants/lib/tail.js');
  const scan = await scanTail(apiBase(), await recentStart(apiBase()));
  return scan.events;
}

export async function renewLease(leaseId: string, ttlSeconds?: number): Promise<{ status: number; body: unknown }> {
  const attempt = await fetch(`${api()}/leases/${leaseId}/renew`, {
    method: 'POST',
    headers: { ...headers(), 'Idempotency-Key': newIdempotencyKey(`e2e-renew-${leaseId.slice(-8)}`) },
    body: JSON.stringify(ttlSeconds === undefined ? {} : { ttl_seconds: ttlSeconds }),
  });
  const raw = await attempt.text();
  const parsedUnknown: unknown = raw.length === 0 ? null : JSON.parse(raw);
  return { status: attempt.status, body: parsedUnknown };
}

/** DELETE /leases/{id} — documented idempotent release. */
export async function releaseLease(leaseId: string): Promise<number> {
  const res = await fetch(`${api()}/leases/${leaseId}`, { method: 'DELETE', headers: headers() });
  await res.text();
  return res.status;
}

export function parseError(body: unknown): ErrorEnvelope {
  const parsed = errorEnvelopeSchema.safeParse(body);
  if (!parsed.success) throw new Error(`expected ErrorEnvelope, got: ${JSON.stringify(body)}`);
  return parsed.data;
}

export { leaseRenewalSchema, newIdempotencyKey };
