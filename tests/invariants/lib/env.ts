import { z } from 'zod';

/**
 * Env contract for the harness. Parsed once; missing live-mode env is a
 * clean skip, malformed env fails loudly (charter: validate at boundary).
 */
const envSchema = z.object({
  CISYNC_API_URL: z.string().url().optional(),
  CISYNC_FLEET_URL: z.string().url().optional(),
  CISYNC_INGEST_URL: z.string().url().optional(),
  CISYNC_ADMIN_TOKEN: z.string().min(1).default('dev_admin_token_not_for_prod'),
  CISYNC_WEBHOOK_SECRET: z.string().min(1).default('dev_webhook_secret_not_for_prod'),
  CISYNC_E2E: z.string().optional(),
});

export interface HarnessEnv {
  apiUrl: string | undefined;
  fleetUrl: string;
  ingestUrl: string;
  adminToken: string;
  webhookSecret: string;
  e2eEnabled: boolean;
}

let cached: HarnessEnv | undefined;

export function harnessEnv(): HarnessEnv {
  if (cached) return cached;
  const parsed = envSchema.safeParse(process.env);
  if (!parsed.success) {
    const issues = parsed.error.issues.map((i) => `${i.path.join('.')}: ${i.message}`).join('; ');
    throw new Error(`invalid CISYNC_* environment [${issues}]`);
  }
  const e = parsed.data;
  cached = {
    apiUrl: e.CISYNC_API_URL,
    fleetUrl: e.CISYNC_FLEET_URL ?? 'http://localhost:8082',
    ingestUrl: e.CISYNC_INGEST_URL ?? 'http://localhost:8080',
    adminToken: e.CISYNC_ADMIN_TOKEN,
    webhookSecret: e.CISYNC_WEBHOOK_SECRET,
    // E2E suites drive a full compose stack; they only run when explicitly
    // opted in so plain `pnpm test` never requires docker.
    e2eEnabled: e.CISYNC_E2E === '1',
  };
  return cached;
}

export function liveModeEnabled(): boolean {
  return harnessEnv().apiUrl !== undefined;
}
