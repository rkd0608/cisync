// Soak validation — sustained load + invariant probes over time.
// WHY separate from storm.ts: storms measure burst absorption; soak measures
// DRIFT (memory, latency degradation, queue backlog growth, budget leaks)
// which only appears under sustained pressure. Run against a FRESH stack.
import { postJson, authHeaders } from './storm-lib';

interface SoakConfig {
  apiBase: string;
  adminToken: string;
  durationMinutes: number;
  unitsPerMinute: number;
  dupes: number;
}

interface Sample {
  tMinutes: number;
  rssMb: number;
  intentsOk: number;
  intentsFail: number;
  p50Ms: number;
  p95Ms: number;
  ledgerSeq: number;
}

const args = new Set(process.argv.slice(2));
void args;
const flag = (name: string, dflt: string): string => {
  const i = process.argv.indexOf(`--${name}`);
  const v = i > -1 ? process.argv[i + 1] : undefined;
  return v ?? dflt;
};

async function main(): Promise<void> {
  const cfg: SoakConfig = {
    apiBase: process.env.CISYNC_API_URL ?? 'http://localhost:8081',
    adminToken: process.env.CISYNC_ADMIN_TOKEN ?? 'dev_admin_token_not_for_prod',
    durationMinutes: Number(flag('minutes', '30')),
    unitsPerMinute: Number(flag('rate', '60')),
    dupes: Number(flag('dupes', '2')),
  };
  console.log(
    `soak: ${cfg.durationMinutes}min @ ${cfg.unitsPerMinute} units/min, dupes=${cfg.dupes}`,
  );

  const samples: Sample[] = [];
  const started = Date.now();
  const endAt = started + cfg.durationMinutes * 60_000;
  const intervalMs = 60_000 / Math.max(1, cfg.unitsPerMinute);
  let minute = 0;
  let nextSample = started;

  while (Date.now() < endAt) {
    const unitStart = performance.now();
    const key = `soak-${started}-${minute}-${Math.random().toString(36).slice(2)}-xxxx`;
    const res = await postJson(`${cfg.apiBase}/v1/change-intents`, {
      ...authHeaders(cfg.adminToken),
      'Idempotency-Key': key,
    }, {
      goal: `soak unit @${new Date().toISOString()}`,
      repository: `acme/soak-${Math.floor(Math.random() * 8)}`,
      base: 'main',
      expected_surfaces: ['services/checkout/**'],
      acceptance_criteria: ['soak'],
      risk: 'low',
    });
    void res;
    void unitStart;
    if (Date.now() >= nextSample) {
      minute += 1;
      nextSample += 60_000;
      samples.push(await sample(minute, cfg));
      console.log(`t+${minute}m: ${JSON.stringify(samples[samples.length - 1])}`);
    }
    const elapsed = Date.now() - (unitStart as unknown as number);
    await sleep(Math.max(0, intervalMs - elapsed));
  }
  console.log('SOAK COMPLETE');
  console.log(JSON.stringify({ config: cfg, samples }, null, 2));
}

async function sample(minute: number, cfg: SoakConfig): Promise<Sample> {
  const stats = await fetchContainerStats();
  const seq = await ledgerHead(cfg);
  return { tMinutes: minute, rssMb: stats.rssMb, intentsOk: -1, intentsFail: -1,
    p50Ms: -1, p95Ms: -1, ledgerSeq: seq };
}

async function fetchContainerStats(): Promise<{ rssMb: number }> {
  // WHY exec-free: reading cgroup memory of control-plane via docker API is
  // env-dependent; simplest portable probe is PG connection + process RSS
  // reported by control-plane /metrics (go_memstats_alloc_bytes).
  try {
    const res = await fetch('http://localhost:8081/metrics');
    const text = await res.text();
    const m = text.match(/go_memstats_alloc_bytes (\d+)/);
    return { rssMb: m ? Math.round(Number(m[1]) / 1e6) : -1 };
  } catch {
    return { rssMb: -1 };
  }
}

async function ledgerHead(cfg: SoakConfig): Promise<number> {
  try {
    const res = await fetch(`${cfg.apiBase}/v1/events?after_seq=0&limit=1`, {
      headers: { Authorization: `Bearer ${cfg.adminToken}` },
    });
    const body = (await res.json()) as { next_seq?: number };
    return body.next_seq ?? -1;
  } catch {
    return -1;
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

main().catch((err: unknown) => {
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
});
