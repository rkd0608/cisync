import { createHmac } from 'node:crypto';
import { drainTailFor } from './journey.js';
import { harnessEnv } from '../../invariants/lib/env.js';

/**
 * Webhook posting helpers (ingest :8080 → control-plane per
 * internal-protocols §1). Signatures use the shared dev secret.
 */

export function webhookSecret(): string {
  return harnessEnv().webhookSecret;
}

export function ingestBase(): string {
  return harnessEnv().ingestUrl.replace(/\/$/, '');
}

export async function postSignedWebhook(
  deliveryGuid: string,
  rawBody: string,
  secretOverride?: string,
): Promise<{ status: number; bodyText: string }> {
  const secret = secretOverride ?? webhookSecret();
  const signature = 'sha256=' + createHmac('sha256', secret).update(rawBody).digest('hex');
  const res = await fetch(`${ingestBase()}/hooks/github`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Hub-Signature-256': signature,
      'X-GitHub-Delivery': deliveryGuid,
      'X-GitHub-Event': 'push',
    },
    body: rawBody,
  });
  return { status: res.status, bodyText: await res.text() };
}

export { drainTailFor };
