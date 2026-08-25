import { describe, expect, it } from 'vitest';
import {
  installationsStatusResponseSchema,
  anyRepoReceiving,
  stalledRepos,
  type InstallationStatus,
} from './installation-schemas';

const receivingRepo = {
  name: 'payments',
  webhook_state: 'receiving',
  last_delivery_seq: 48112,
  last_event_at: '2026-08-23T03:00:12Z',
} as const;

const validInstallation: InstallationStatus = {
  installation_id: 48211,
  account: 'acme',
  repos: [
    receivingRepo,
    { name: 'legacy-api', webhook_state: 'stalled', last_delivery_seq: 40981,
      last_event_at: '2026-08-23T02:26:00Z' },
  ],
  permissions: { checks: 'write', contents: 'read', pull_requests: 'read' },
};

describe('installationsStatusResponseSchema (G3)', () => {
  it('accepts the documented shape', () => {
    const parsed = installationsStatusResponseSchema.safeParse({
      installations: [validInstallation],
    });
    expect(parsed.success).toBe(true);
  });

  it('rejects webhook states outside the pending|receiving|stalled enum', () => {
    const broken = {
      ...validInstallation,
      repos: [{ ...receivingRepo, webhook_state: 'vibrating' }],
    };
    expect(
      installationsStatusResponseSchema.safeParse({ installations: [broken] }).success,
    ).toBe(false);
  });

  it('rejects non-numeric installation ids (github ids are integers)', () => {
    const broken = { ...validInstallation, installation_id: 'inst_48211' };
    expect(
      installationsStatusResponseSchema.safeParse({ installations: [broken] }).success,
    ).toBe(false);
  });

  it('rejects a missing repos array', () => {
    const { repos: _omitted, ...broken } = validInstallation;
    expect(
      installationsStatusResponseSchema.safeParse({ installations: [broken] }).success,
    ).toBe(false);
  });

  it('accepts nullable seq/timestamps and an empty list', () => {
    const parsed = installationsStatusResponseSchema.safeParse({
      installations: [
        { ...validInstallation, repos: [{ name: 'x', webhook_state: 'pending',
          last_delivery_seq: null, last_event_at: null }] },
      ],
    });
    expect(parsed.success).toBe(true);
    expect(installationsStatusResponseSchema.safeParse({ installations: [] }).success).toBe(true);
  });
});

describe('stalledRepos / anyRepoReceiving helpers', () => {
  const installations: InstallationStatus[] = [validInstallation];

  it('lists only stalled repos with their account', () => {
    expect(stalledRepos(installations)).toEqual([
      {
        account: 'acme',
        repo: validInstallation.repos[1],
      },
    ]);
  });

  it('detects the onboarding completion signal', () => {
    expect(anyRepoReceiving(installations)).toBe(true);
    expect(anyRepoReceiving([{ ...validInstallation, repos: [] }])).toBe(false);
    expect(
      anyRepoReceiving([
        { ...validInstallation, repos: [{ name: 'docs', webhook_state: 'pending' }] },
      ]),
    ).toBe(false);
  });
});
