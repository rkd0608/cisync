// G3 GET /v1/installations/status (PRODUCT_UX_PLAN §6). Proves the webhook
// pipe is alive; first debugging stop when checks don't appear. Split from
// api-schemas.ts to keep both files under the charter line cap (same pattern
// as event-schemas.ts).
import { z } from 'zod';

export const WEBHOOK_STATES = ['pending', 'receiving', 'stalled'] as const;
export const webhookStateSchema = z.enum(WEBHOOK_STATES);
export type WebhookState = (typeof WEBHOOK_STATES)[number];

export const repoWebhookStatusSchema = z.object({
  name: z.string(),
  webhook_state: webhookStateSchema,
  last_delivery_seq: z.number().int().nonnegative().nullable().optional(),
  last_event_at: z.string().nullable().optional(),
});

export const installationStatusSchema = z.object({
  // GitHub installation ids are numeric app-installation identifiers.
  installation_id: z.number().int().positive(),
  account: z.string(),
  repos: z.array(repoWebhookStatusSchema),
  permissions: z.record(z.string()).optional(),
});

export const installationsStatusResponseSchema = z.object({
  installations: z.array(installationStatusSchema),
});

export type RepoWebhookStatus = z.infer<typeof repoWebhookStatusSchema>;
export type InstallationStatus = z.infer<typeof installationStatusSchema>;
export type InstallationsStatusResponse = z.infer<
  typeof installationsStatusResponseSchema
>;

// True when the pipe has gone quiet for one repo (stalled) anywhere in the
// tenant — drives the red-dot warning rows on /installations.
export function stalledRepos(
  installations: InstallationStatus[],
): Array<{ account: string; repo: RepoWebhookStatus }> {
  return installations.flatMap((installation) =>
    installation.repos
      .filter((repo) => repo.webhook_state === 'stalled')
      .map((repo) => ({ account: installation.account, repo })),
  );
}

// Onboarding step-2 completion signal: at least one repo is receiving events.
export function anyRepoReceiving(installations: InstallationStatus[]): boolean {
  return installations.some((installation) =>
    installation.repos.some((repo) => repo.webhook_state === 'receiving'),
  );
}
