// Render-state tests for the ops surfaces (W5/C6+C8): installations table
// states and the stale-feed indicator. react-dom/server only — no jsdom
// (proposed, awaiting approval).
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { InstallationsTable } from './installations-table';
import { StaleFeedBannerView } from './stale-feed-banner';
import type { InstallationStatus } from '@/lib/installation-schemas';

const installation: InstallationStatus = {
  installation_id: 48211,
  account: 'acme',
  repos: [
    { name: 'payments', webhook_state: 'receiving', last_delivery_seq: 48112,
      last_event_at: '2026-08-23T11:59:48Z' },
    { name: 'legacy-api', webhook_state: 'stalled', last_delivery_seq: 40981,
      last_event_at: '2026-08-23T11:26:00Z' },
  ],
  permissions: { checks: 'write', contents: 'read' },
};

describe('InstallationsTable render states (§2.2)', () => {
  it('marks stalled rows with red styling; ages stay deterministic pre-mount', () => {
    const html = renderToStaticMarkup(
      <InstallationsTable
        data={{ installations: [installation] }}
        error={null}
        syncing={false}
        onResync={() => undefined}
        nowMs={null}
      />,
    );
    expect(html).toContain('data-webhook-state="stalled"');
    expect(html).toContain('bg-red-950/20');
    expect(html).toContain('⚠ stalled');
    // Ages are '--' until the client clock mounts (no SSR hydration drift).
    expect(html).not.toContain('34m ago');
    expect(html).toContain('seq 48,112');
    expect(html).toContain('checks:write');
  });

  it('empty state teaches and routes to /onboarding', () => {
    const html = renderToStaticMarkup(
      <InstallationsTable data={{ installations: [] }} error={null} syncing={false} onResync={() => undefined} nowMs={null} />,
    );
    expect(html).toContain('no installations');
    expect(html).toContain('href="/onboarding"');
  });

  it('error state surfaces the machine-readable code', () => {
    const html = renderToStaticMarkup(
      <InstallationsTable
        data={null}
        error={{ code: 'unavailable', message: 'control-plane unreachable' }}
        syncing={false}
        onResync={() => undefined}
        nowMs={null}
      />,
    );
    expect(html).toContain('installations-error');
    expect(html).toContain('unavailable');
    expect(html).toContain('resync');
  });

  it('syncing state disables resync instead of stacking requests', () => {
    const html = renderToStaticMarkup(
      <InstallationsTable data={null} error={null} syncing onResync={() => undefined} nowMs={null} />,
    );
    expect(html).toContain('resyncing…');
    expect(html).toContain('disabled');
  });
});

describe('stale-feed banner view (>60s seq stall)', () => {
  it('hides while the feed is fresh', () => {
    expect(renderToStaticMarkup(<StaleFeedBannerView stale={false} lastSeq={48112} />)).toBe('');
  });

  it('cites the last held seq when stalled (§7 latency contract)', () => {
    const html = renderToStaticMarkup(<StaleFeedBannerView stale lastSeq={48112} />);
    expect(html).toContain('live feed paused — showing snapshot at seq 48112');
  });
});
