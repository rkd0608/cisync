import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { createHmac } from 'node:crypto';
import {
  authHeaders,
  intentUnit,
  mulberry32,
  postJson,
  runPool,
  LatencyRecorder,
  type Histogram,
  type OpOutcome,
  type StormConfig,
} from './storm-lib.js';

/**
 * Concurrency storm against a RUNNING CISync stack (compose-up required).
 *   pnpm exec tsx storm.ts --concurrency 500 --repos 8 --dupes 4
 * Drives N intents × M near-duplicate candidates across K repos through the
 * PUBLIC API only; collects latency histograms + error-class counts and runs
 * inline invariant probes. `--chaos` arms W3 fault hooks (cancel storms +
 * base-advance stampede). Writes JSON reports to scenarios/reports/.
 */

interface CliOptions {
  concurrency: number;
  repos: number;
  dupes: number;
  chaos: boolean;
}

function parseArgs(argv: readonly string[]): CliOptions {
  const get = (flag: string): string | undefined => {
    const i = argv.indexOf(flag);
    return i >= 0 ? argv[i + 1] : undefined;
  };
  const num = (value: string | undefined, fallback: number): number => {
    const parsed = Number.parseInt(value ?? '', 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
  };
  return {
    concurrency: num(get('--concurrency'), 500),
    repos: num(get('--repos'), 8),
    dupes: num(get('--dupes'), 4),
    chaos: argv.includes('--chaos'),
  };
}

function envConfig(opts: CliOptions): StormConfig {
  return {
    ...opts,
    apiBase: (process.env['CISYNC_API_URL'] ?? 'http://localhost:8081').replace(/\/$/, ''),
    ingestBase: (process.env['CISYNC_INGEST_URL'] ?? 'http://localhost:8080').replace(/\/$/, ''),
    adminToken: process.env['CISYNC_ADMIN_TOKEN'] ?? 'dev_admin_token_not_for_prod',
    webhookSecret: process.env['CISYNC_WEBHOOK_SECRET'] ?? 'dev_webhook_secret_not_for_prod',
    seed: Number.parseInt(process.env['SCENARIO_SEED'] ?? '', 10) || 42,
  };
}

async function assertHealthy(cfg: StormConfig): Promise<void> {
  try {
    const res = await fetch(`${cfg.apiBase}/healthz`);
    if (!res.ok) throw new Error(`status ${res.status}`);
  } catch (err) {
    throw new Error(`control-plane ${cfg.apiBase} is not reachable (${err instanceof Error ? err.message : err}); run make up first`);
  }
}

/** I-14 spot check: well-formed foreign id must be an anonymous 404. */
async function probeUniform404(cfg: StormConfig): Promise<{ name: string; pass: boolean; detail: string }> {
  const foreign = `cand_0${'A'.repeat(25)}`;
  const res = await fetch(`${cfg.apiBase}/v1/candidates/${foreign}`, { headers: authHeaders(cfg.adminToken) });
  const body = (await res.json().catch(() => null)) as { error?: { code?: string } } | null;
  const pass = res.status === 404 && body?.error?.code === 'not_found';
  return { name: 'uniform_404_cross_tenant', pass, detail: `status=${res.status} code=${body?.error?.code ?? 'none'}` };
}

/** I-12 spot check: identical replay must return the identical body. */
async function probeIdempotentReplay(cfg: StormConfig): Promise<{ name: string; pass: boolean; detail: string }> {
  const key = `storm-replay-${Date.now()}`;
  const body = {
    goal: 'storm idempotency probe', repository: 'acme/storm-probe', base: 'main',
    expected_surfaces: ['services/**'], acceptance_criteria: ['probe'], risk: 'low',
  };
  const first = await postJson(`${cfg.apiBase}/v1/change-intents`, { ...authHeaders(cfg.adminToken), 'Idempotency-Key': key }, body);
  const second = await postJson(`${cfg.apiBase}/v1/change-intents`, { ...authHeaders(cfg.adminToken), 'Idempotency-Key': key }, body);
  const pass = first.status === second.status && JSON.stringify(first.json) === JSON.stringify(second.json);
  return { name: 'idempotent_replay_identical', pass, detail: `${first.status}/${second.status}` };
}

/** CHAOS HOOK (W3): cancel storm — releases leases concurrently to race
 *  completion/expiry paths (EC-030, EC-042). */
export async function chaosCancelStorm(cfg: StormConfig, leaseIds: readonly string[]): Promise<number> {
  let cancelled = 0;
  await runPool(leaseIds, Math.min(64, leaseIds.length), async (leaseId) => {
    const res = await fetch(`${cfg.apiBase}/v1/leases/${String(leaseId)}`, { method: 'DELETE', headers: authHeaders(cfg.adminToken) });
    if (res.ok) cancelled += 1;
  });
  return cancelled;
}

/** CHAOS HOOK (W3): base-advance stampede — signed push webhooks per repo to
 *  trigger merge_base.advanced batch invalidation (EC-026). */
export async function chaosBaseAdvanceStampede(cfg: StormConfig, repos: number): Promise<number> {
  let sent = 0;
  for (let r = 0; r < repos; r++) {
    const raw = JSON.stringify({ ref: 'refs/heads/main', after: 'f'.repeat(40), repository: { full_name: `acme/storm-${r}` } });
    const signature = 'sha256=' + createHmac('sha256', cfg.webhookSecret).update(raw).digest('hex');
    const res = await fetch(`${cfg.ingestBase}/hooks/github`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Hub-Signature-256': signature, 'X-GitHub-Delivery': `chaos-${Date.now()}-${r}`, 'X-GitHub-Event': 'push' },
      body: raw,
    });
    if (res.ok) sent += 1;
  }
  return sent;
}

interface StormReport {
  startedAt: string;
  finishedAt: string;
  config: Pick<StormConfig, 'concurrency' | 'repos' | 'dupes' | 'chaos' | 'seed'>;
  totals: Record<string, number>;
  latencyMs: Record<string, Histogram>;
  errorClasses: Record<string, number>;
  probes: Array<{ name: string; pass: boolean; detail: string }>;
  chaos?: Record<string, number>;
}

async function main(): Promise<void> {
  const cfg = envConfig(parseArgs(process.argv.slice(2)));
  await assertHealthy(cfg);
  console.log(`storm: concurrency=${cfg.concurrency} repos=${cfg.repos} dupes=${cfg.dupes} chaos=${cfg.chaos} seed=${cfg.seed}`);

  const rand = mulberry32(cfg.seed);
  const latency = new LatencyRecorder();
  const candidateLatency = new LatencyRecorder();
  const errors: Record<string, number> = {};
  const totals = { intents_ok: 0, candidates_ok: 0 };
  const leasesSeen: string[] = [];
  const units = Array.from({ length: cfg.concurrency }, (_, i) => i);

  const startedAt = new Date().toISOString();
  await runPool(units, Math.min(cfg.concurrency, 128), async (unit) => {
    for (const outcome of await intentUnit(cfg, rand, unit)) {
      collect(outcome);
    }
  });
  function collect(outcome: OpOutcome): void {
    if (outcome.kind === 'create_intent') {
      latency.record(outcome.ms);
      if (outcome.ok) {
        totals.intents_ok += 1;
        if (outcome.leaseId) leasesSeen.push(outcome.leaseId);
      } else if (outcome.errorClass) errors[outcome.errorClass] = (errors[outcome.errorClass] ?? 0) + 1;
    } else {
      candidateLatency.record(outcome.ms);
      if (outcome.ok) totals.candidates_ok += 1;
      else if (outcome.errorClass) errors[outcome.errorClass] = (errors[outcome.errorClass] ?? 0) + 1;
    }
  }

  // WHY: probes must never prevent report persistence — the report IS the
  // evidence artifact of a storm run; a crashing probe loses the whole run.
  const probes = await Promise.allSettled([probeUniform404(cfg), probeIdempotentReplay(cfg)]);
  const probeResults = probes.map((r, i) =>
    r.status === 'fulfilled'
      ? r.value
      : { name: `probe-${i}`, pass: false, detail: `threw: ${String(r.reason).slice(0, 120)}` },
  );
  const report: StormReport = {
    startedAt,
    finishedAt: new Date().toISOString(),
    config: { concurrency: cfg.concurrency, repos: cfg.repos, dupes: cfg.dupes, chaos: cfg.chaos, seed: cfg.seed },
    totals,
    latencyMs: { create_intent: latency.histogram(), submit_candidate: candidateLatency.histogram() },
    errorClasses: errors,
    probes: probeResults,
  };

  if (cfg.chaos) {
    // Experimental until W3 wires fault injection end-to-end.
    report.chaos = {
      leases_cancelled: await chaosCancelStorm(cfg, leasesSeen),
      push_webhooks_sent: await chaosBaseAdvanceStampede(cfg, cfg.repos),
    };
  }

  const dir = join(import.meta.dirname ?? '.', 'reports');
  mkdirSync(dir, { recursive: true });
  const file = join(dir, `storm-${Date.now()}.json`);
  writeFileSync(file, JSON.stringify(report, null, 2) + '\n');

  console.log(`intents ok=${totals.intents_ok} candidates ok=${totals.candidates_ok}`);
  console.log(`create_intent p50/p95/p99 ms: ${report.latencyMs['create_intent']?.p50_ms}/${report.latencyMs['create_intent']?.p95_ms}/${report.latencyMs['create_intent']?.p99_ms}`);
  console.log(`errors: ${JSON.stringify(errors)}`);
  for (const probe of probeResults) console.log(`${probe.pass ? 'PASS' : 'FAIL'} ${probe.name}: ${probe.detail}`);
  console.log(`report written: ${file}`);
  if (probeResults.some((p) => !p.pass)) process.exitCode = 1;
}

main().catch((err: unknown) => {
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
});
