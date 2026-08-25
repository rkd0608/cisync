import Link from 'next/link';
import type { ReactElement } from 'react';
import { EmptyState } from './empty-state';
import type {
  InstallationStatus,
  InstallationsStatusResponse,
  RepoWebhookStatus,
} from '@/lib/installation-schemas';
import { relativeAge } from '@/lib/format';

const STATE_DOT: Record<RepoWebhookStatus['webhook_state'], string> = {
  receiving: 'bg-emerald-400',
  pending: 'border border-zinc-500 bg-transparent',
  stalled: 'animate-pulse bg-red-500',
};

function RepoRow({ repo, nowMs }: { repo: RepoWebhookStatus; nowMs: number | null }): ReactElement {
  const stalled = repo.webhook_state === 'stalled';
  return (
    <tr
      data-repo={repo.name}
      data-webhook-state={repo.webhook_state}
      className={`border-b border-zinc-900 last:border-0 ${stalled ? 'bg-red-950/20' : ''}`}
    >
      <td className="py-1.5 pl-6 pr-2 font-mono text-xs text-zinc-200">
        <span className={`mr-2 inline-block h-1.5 w-1.5 rounded-full ${STATE_DOT[repo.webhook_state]}`} aria-hidden />
        {repo.name}
        {stalled ? <span className="ml-2 text-red-400">⚠ stalled</span> : null}
      </td>
      <td className="py-1.5 pr-2 text-right font-mono text-xs tabular-nums text-zinc-400">
        seq {repo.last_delivery_seq?.toLocaleString('en-US') ?? '--'}
      </td>
      {/* Age is client-only (relative to now) to keep SSR markup deterministic. */}
      <td
        data-testid="delivery-age"
        title={repo.last_event_at ?? undefined}
        className={`py-1.5 text-right font-mono text-xs tabular-nums ${stalled ? 'text-red-300' : 'text-zinc-500'}`}
      >
        {nowMs === null ? '--' : relativeAge(repo.last_event_at, nowMs)}
      </td>
    </tr>
  );
}

function InstallationBlock({
  installation,
  nowMs,
}: {
  installation: InstallationStatus;
  nowMs: number | null;
}): ReactElement {
  return (
    <section className="rounded border border-zinc-800 bg-zinc-950 px-4 py-3">
      <p className="flex flex-wrap items-center gap-2 font-mono text-xs text-zinc-300">
        <span>{installation.account}</span>
        <span className="text-emerald-400">app installed ✓</span>
        {Object.entries(installation.permissions ?? {}).map(([permission, level]) => (
          <span key={permission} className="text-zinc-600">
            {permission}:{level}
          </span>
        ))}
      </p>
      <table className="mt-2 w-full border-collapse font-mono text-xs">
        <thead>
          <tr className="border-b border-zinc-800 text-left text-[10px] uppercase tracking-widest text-zinc-600">
            <th className="py-1 pr-2">repo</th>
            <th className="py-1 pr-2 text-right">last delivery</th>
            <th className="py-1 text-right">age</th>
          </tr>
        </thead>
        <tbody>
          {installation.repos.map((repo) => (
            <RepoRow key={repo.name} repo={repo} nowMs={nowMs} />
          ))}
        </tbody>
      </table>
    </section>
  );
}

export interface InstallationsTableProps {
  data: InstallationsStatusResponse | null;
  error: ApiErrorLike | null;
  syncing: boolean;
  onResync: () => void;
  // Wall-clock for relative ages; null keeps SSR markup deterministic (the
  // client shell fills it in after mount).
  nowMs: number | null;
}

// Shape-compatible with ErrorState's view without importing the server-side
// error component into this presentational module.
export interface ApiErrorLike {
  code: string;
  message: string;
}

// Dense status table (§2.2): proves the pipe is alive; red-dot rows mark
// stalled deliveries; resync refetches only — never mutates installations.
export function InstallationsTable({
  data,
  error,
  syncing,
  onResync,
  nowMs,
}: InstallationsTableProps): ReactElement {
  const installations = data?.installations ?? [];
  return (
    <div className="flex flex-col gap-4" data-testid="installations-table">
      <div className="flex items-center justify-between">
        <h2 className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          installations · webhook pipe health
        </h2>
        <button
          type="button"
          onClick={onResync}
          disabled={syncing}
          className="rounded border border-zinc-700 px-3 py-1 font-mono text-[11px] uppercase tracking-wider text-zinc-200 hover:bg-zinc-800 disabled:opacity-40"
        >
          {syncing ? 'resyncing…' : 'resync'}
        </button>
      </div>

      {error !== null ? (
        <div role="alert" data-testid="installations-error" className="rounded border border-red-900/60 bg-red-950/30 px-4 py-3 font-mono text-xs text-red-200">
          <span className="rounded bg-red-500/20 px-1.5 py-0.5 uppercase tracking-wider">{error.code}</span>{' '}
          {error.message}
        </div>
      ) : null}

      {error === null && !syncing && installations.length === 0 ? (
        <EmptyState
          what="no installations"
          whyEmpty="No GitHub App installation is connected to this tenant yet."
          action={{ label: 'start at /onboarding', href: '/onboarding' }}
        />
      ) : null}

      {!error && installations.map((installation) => (
        <InstallationBlock key={installation.installation_id} installation={installation} nowMs={nowMs} />
      ))}

      <p className="font-mono text-[10px] uppercase tracking-widest text-zinc-700">
        first debugging stop when checks don&apos;t appear · <Link href="/onboarding" className="underline">onboarding</Link>
      </p>
    </div>
  );
}
