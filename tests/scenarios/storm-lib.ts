import { errorEnvelopeSchema } from '../invariants/lib/api-schemas.js';

/**
 * Storm library: config parsing, bounded-concurrency execution, latency
 * histograms, error classification and inline invariant probes. Consumed by
 * storm.ts (W3 runs this nightly against a compose stack).
 */

export interface StormConfig {
  concurrency: number;
  repos: number;
  dupes: number;
  apiBase: string;
  ingestBase: string;
  adminToken: string;
  webhookSecret: string;
  chaos: boolean;
  seed: number;
}

export interface Histogram {
  count: number;
  mean_ms: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  max_ms: number;
}

export class LatencyRecorder {
  private readonly samples: number[] = [];
  record(ms: number): void {
    this.samples.push(ms);
  }
  histogram(): Histogram {
    const sorted = [...this.samples].sort((a, b) => a - b);
    const at = (q: number): number => sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))] ?? 0;
    const total = sorted.reduce((acc, v) => acc + v, 0);
    return {
      count: sorted.length,
      mean_ms: sorted.length === 0 ? 0 : Math.round(total / sorted.length),
      p50_ms: at(0.5),
      p95_ms: at(0.95),
      p99_ms: at(0.99),
      max_ms: sorted.at(-1) ?? 0,
    };
  }
}

/** Deterministic PRNG so failures reproduce bit-for-bit (TEST_STRATEGY §1.S). */
export function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const sha40 = (rand: () => number): string =>
  Array.from({ length: 40 }, () => Math.floor(rand() * 16).toString(16)).join('');

export interface OpOutcome {
  kind: 'create_intent' | 'submit_candidate';
  ok: boolean;
  ms: number;
  errorClass?: string;
  leaseId?: string;
  intentId?: string;
}

export async function postJson(
  url: string,
  headers: Record<string, string>,
  body: unknown,
): Promise<{ status: number; json: unknown }> {
  const res = await fetch(url, { method: 'POST', headers, body: JSON.stringify(body) });
  const text = await res.text();
  let json: unknown = null;
  try {
    json = text.length === 0 ? null : JSON.parse(text);
  } catch {
    json = { unparsable: text.slice(0, 120) };
  }
  return { status: res.status, json };
}

export function classifyError(status: number, json: unknown): string {
  if (status === 0 || status >= 500) return `http_${status || 'network'}`;
  const parsed = errorEnvelopeSchema.safeParse(json);
  const code = parsed.success ? parsed.data.error.code : 'untyped';
  return `http_${status}_${code}`;
}

export function authHeaders(token: string): Record<string, string> {
  return { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };
}

/** One intent + its near-duplicate candidates, run under the shared limiter. */
export async function intentUnit(
  cfg: StormConfig,
  rand: () => number,
  unitIndex: number,
): Promise<OpOutcome[]> {
  const outcomes: OpOutcome[] = [];
  const repo = `acme/storm-${unitIndex % cfg.repos}`;
  const key = `storm-${Date.now()}-${unitIndex}-${Math.floor(rand() * 1e9)}`;
  const intentHeaders = { ...authHeaders(cfg.adminToken), 'Idempotency-Key': key };

  const t0 = performance.now();
  const created = await postJson(`${cfg.apiBase}/v1/change-intents`, intentHeaders, {
    goal: `storm unit ${unitIndex}`,
    repository: repo,
    base: 'main',
    expected_surfaces: ['services/checkout/**'],
    acceptance_criteria: [`storm-${unitIndex}`],
    risk: rand() > 0.5 ? 'low' : 'medium',
  });
  const intentMs = performance.now() - t0;

  if (created.status !== 200 && created.status !== 201) {
    outcomes.push({ kind: 'create_intent', ok: false, ms: intentMs, errorClass: classifyError(created.status, created.json) });
    return outcomes;
  }
  outcomes.push({
    kind: 'create_intent', ok: true, ms: intentMs,
    intentId: (created.json as { intent_id?: string }).intent_id,
    leaseId: (created.json as { lease_id?: string }).lease_id,
  });

  const intentId = (created.json as { intent_id?: string }).intent_id ?? '';
  // Near-duplicates: identical surface, tiny head drift — tournament fuel.
  const candidateJobs = Array.from({ length: cfg.dupes }, (_, d) => d);
  const submitted = await Promise.all(candidateJobs.map(async (d) => {
    const tC = performance.now();
    const res = await postJson(`${cfg.apiBase}/v1/change-intents/${intentId}/candidates`, authHeaders(cfg.adminToken), {
      patch_ref: `bundle:storm-${unitIndex}`,
      head_sha: sha40(rand),
      base_sha: sha40(rand),
      changed_paths: ['services/checkout/cart.go'],
      ...(d === 0 ? {} : {}),
    });
    const ms = performance.now() - tC;
    if (res.status === 201 || res.status === 200) {
      return { kind: 'submit_candidate', ok: true, ms } as OpOutcome;
    }
    void d;
    return { kind: 'submit_candidate', ok: false, ms, errorClass: classifyError(res.status, res.json) } as OpOutcome;
  }));
  outcomes.push(...submitted);
  return outcomes;
}

/** Bounded-concurrency driver: units flow through `concurrency` workers. */
export async function runPool<T>(items: readonly T[], limit: number, worker: (item: T) => Promise<void>): Promise<void> {
  let cursor = 0;
  const runners = Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, async () => {
    for (;;) {
      const index = cursor++;
      const item = items[index];
      if (item === undefined) break;
      try {
        await worker(item);
      } catch (err) {
        // WHY swallow-and-tag: the storm must observe system behavior, not
        // crash on a single transport hiccup; failures land in the report.
        console.error(`storm unit failed: ${err instanceof Error ? err.message : String(err)}`);
      }
    }
  });
  await Promise.all(runners);
}
