'use client';

import type { ReactElement } from 'react';
import { DisclosurePanel } from '@/components/disclosure-panel';
import type { ConnectStatus } from '@/lib/setup-machine';
import { MAX_CONNECT_POLLS } from '@/lib/setup-machine';

// WHY the install URL is read here and not server-side: the CTA deep-links to
// GitHub's app-install page; env absence renders an honest operator note
// instead of a fabricated link.
const GITHUB_APP_INSTALL_URL = process.env.NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL;

export interface ConnectedRepo {
  account: string;
  name: string;
  webhookState: string;
}

export function ConnectStep({
  connect,
  polls,
  repos,
  onInstalled,
  onRetry,
}: {
  connect: ConnectStatus;
  polls: number;
  repos: ConnectedRepo[];
  onInstalled: () => void;
  onRetry: () => void;
}): ReactElement {
  return (
    <section className="flex flex-col gap-4" data-testid="setup-connect-step">
      <DisclosurePanel />
      {GITHUB_APP_INSTALL_URL ? (
        <div className="flex flex-col items-start gap-2 sm:flex-row sm:items-center">
          <a href={GITHUB_APP_INSTALL_URL} target="_blank" rel="noreferrer" className="btn-console border-[var(--color-accent)]/50 text-[var(--color-accent-soft)]">
            install github app ↗
          </a>
          <button type="button" onClick={onInstalled} className="btn-console">
            i installed it — check connection →
          </button>
        </div>
      ) : (
        <p data-testid="install-url-missing" className="font-mono text-xs text-amber-300">
          installation url not configured — ask your operator for the github app link
        </p>
      )}

      {repos.length > 0 ? (
        <ul className="flex flex-wrap gap-1.5" data-testid="connected-repos">
          {repos.map((repo) => (
            <li
              key={`${repo.account}/${repo.name}`}
              className={`rounded-full border px-2 py-0.5 font-mono text-[11px] ${
                repo.webhookState === 'receiving'
                  ? 'border-emerald-500/50 text-emerald-300'
                  : 'border-zinc-700 text-zinc-400'
              }`}
            >
              {repo.webhookState === 'receiving' ? '●' : '○'} {repo.account}/{repo.name}
            </li>
          ))}
        </ul>
      ) : null}

      {connect === 'polling' ? (
        <p className="flex items-center gap-2 font-mono text-xs text-zinc-400">
          <span aria-hidden className="inline-block h-2 w-2 animate-pulse rounded-full bg-[var(--color-signal)]" />
          waiting: webhook handshake… ({polls}/{MAX_CONNECT_POLLS})
        </p>
      ) : null}
      {connect === 'awaiting_backend' ? (
        <p role="status" className="rounded-lg border border-amber-500/50 bg-amber-950/20 px-4 py-3 font-mono text-xs text-amber-200">
          installations/status is not reachable yet — events cannot be proven until the backend answers. retry when ready.
        </p>
      ) : null}
      {connect === 'handshake_timeout' ? (
        <p role="status" className="rounded-lg border border-[var(--color-risk-critical)]/60 bg-red-950/20 px-4 py-3 font-mono text-xs text-red-200">
          no repo reached `receiving` within the handshake window. confirm the github app is installed on at least one repo, then retry.
        </p>
      ) : null}
      {connect === 'handshake_timeout' || connect === 'awaiting_backend' ? (
        <button type="button" onClick={onRetry} className="btn-console w-fit">
          retry handshake
        </button>
      ) : null}
    </section>
  );
}
