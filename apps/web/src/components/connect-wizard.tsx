'use client';

import Link from 'next/link';
import { useEffect, useReducer, useState, type ReactElement } from 'react';
import { DisclosurePanel } from './disclosure-panel';
import { PostureReview } from './posture-review';
import { anyRepoReceiving, type InstallationStatus } from '@/lib/installation-schemas';
import {
  initialWizardState,
  reduceWizard,
} from '@/lib/onboarding-machine';
import { getInstallationsStatus } from '@/lib/sauron-api';

const POLL_INTERVAL_MS = 3000;
// Read literally so the Next.js build inlines it; unset ⇒ honest operator note.
const GITHUB_APP_INSTALL_URL = process.env.NEXT_PUBLIC_SAURON_GITHUB_APP_INSTALL_URL;

const STEP_TITLES: Record<number, string> = {
  1: 'install app',
  2: 'verify events',
  3: 'review posture',
};

function StepRail({ current }: { current: number }): ReactElement {
  return (
    <div className="flex gap-4 font-mono text-[11px] uppercase tracking-widest">
      {[1, 2, 3].map((step) => (
        <span
          key={step}
          data-step={step}
          data-current={step === current || undefined}
          data-done={step < current || undefined}
          className={step === current ? 'text-cyan-300' : step < current ? 'text-emerald-400' : 'text-zinc-600'}
        >
          {'①②③'[step - 1]} {STEP_TITLES[step]}
        </span>
      ))}
    </div>
  );
}

export function ConnectWizard(): ReactElement {
  const [state, dispatch] = useReducer(reduceWizard, initialWizardState);
  const [installations, setInstallations] = useState<InstallationStatus[] | null>(null);

  // Step-2 poll loop: self-schedules while (and only while) the machine is in
  // 'polling'; backend absence resolves to the awaiting_backend honest state.
  useEffect(() => {
    if (state.verify !== 'polling') return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    async function poll(): Promise<void> {
      const result = await getInstallationsStatus();
      if (stopped) return;
      const reachable = result.ok;
      if (result.ok) setInstallations(result.data.installations);
      dispatch({
        type: 'status_result',
        anyReceiving: reachable && anyRepoReceiving(result.ok ? result.data.installations : []),
        backendReachable: reachable,
      });
      if (!stopped) timer = setTimeout(() => void poll(), POLL_INTERVAL_MS);
    }

    void poll();
    return () => {
      stopped = true;
      if (timer !== null) clearTimeout(timer);
    };
  }, [state.verify]);

  const linkedRepos =
    installations
      ?.flatMap((installation) =>
        installation.repos
          .filter((repo) => repo.webhook_state === 'receiving')
          .map((repo) => `${installation.account}/${repo.name}`),
      )
      ?? [];

  return (
    <div data-testid="connect-wizard" className="flex flex-col gap-6 rounded border border-zinc-800 bg-zinc-950 px-6 py-5">
      <StepRail current={state.step === 'install' ? 1 : state.step === 'verify' ? 2 : 3} />

      {state.step === 'install' ? (
        <section className="flex flex-col gap-4">
          <DisclosurePanel />
          {GITHUB_APP_INSTALL_URL ? (
            <a
              href={GITHUB_APP_INSTALL_URL}
              target="_blank"
              rel="noreferrer"
              className="w-fit rounded border border-cyan-500/50 px-4 py-2 font-mono text-xs uppercase tracking-wider text-cyan-300 hover:bg-cyan-500/10"
            >
              install github app →
            </a>
          ) : (
            <p data-testid="install-url-missing" className="font-mono text-xs text-amber-300">
              installation url not configured — ask your operator for the github app link
            </p>
          )}
          <button
            type="button"
            onClick={() => dispatch({ type: 'start_verify' })}
            className="w-fit rounded border border-zinc-700 px-4 py-2 font-mono text-xs uppercase tracking-wider text-zinc-200 hover:bg-zinc-800"
          >
            i installed it — verify events →
          </button>
        </section>
      ) : null}

      {state.step === 'verify' ? <VerifyPanel state={state.verify} linkedRepos={linkedRepos} onRetry={() => dispatch({ type: 'retry_verify' })} onContinue={() => dispatch({ type: 'open_posture' })} /> : null}

      {state.step === 'posture' ? (
        <section className="flex flex-col gap-4">
          <PostureReview
            ackChecked={state.ackNoEnforcement}
            onAckChange={(checked) => dispatch({ type: 'ack_no_enforcement', checked })}
          />
          {state.finished ? (
            <p data-testid="wizard-finished" className="font-mono text-xs text-emerald-300">
              setup complete — nothing is enforced yet. head to{' '}
              <Link href="/" className="underline">the board</Link>.
            </p>
          ) : (
            <button
              type="button"
              disabled={!state.ackNoEnforcement}
              onClick={() => dispatch({ type: 'finish' })}
              className="w-fit rounded border border-zinc-700 px-4 py-2 font-mono text-xs uppercase tracking-wider text-zinc-200 enabled:hover:bg-zinc-800 disabled:opacity-40"
            >
              finish → dashboard
            </button>
          )}
        </section>
      ) : null}
    </div>
  );
}

function VerifyPanel({
  state,
  linkedRepos,
  onRetry,
  onContinue,
}: {
  state: ReturnType<typeof reduceWizard>['verify'];
  linkedRepos: string[];
  onRetry: () => void;
  onContinue: () => void;
}): ReactElement {
  return (
    <section className="flex flex-col gap-3" data-testid="verify-panel" data-state={state}>
      {state === 'polling' ? (
        <p className="flex items-center gap-2 font-mono text-xs text-zinc-400">
          <span aria-hidden className="inline-block h-2 w-2 animate-pulse rounded-full bg-cyan-400" />
          waiting: webhook handshake… (events must flow before verification means anything)
        </p>
      ) : null}
      {linkedRepos.length > 0 ? (
        <p className="font-mono text-xs text-emerald-300">
          ● linked: {linkedRepos.join(', ')}
        </p>
      ) : null}
      {state === 'awaiting_backend' ? (
        <p className="rounded border border-amber-500/50 bg-amber-950/20 px-4 py-3 font-mono text-xs text-amber-200">
          awaiting backend — installations/status is not reachable yet; polling will resume on retry.
        </p>
      ) : null}
      {state === 'handshake_timeout' ? (
        <p className="rounded border border-red-500/50 bg-red-950/20 px-4 py-3 font-mono text-xs text-red-200">
          no repo reached `receiving` within the handshake window — check the github app is installed on at least one repo.
        </p>
      ) : null}
      {state === 'handshake_timeout' || state === 'awaiting_backend' ? (
        <button type="button" onClick={onRetry} className="w-fit rounded border border-zinc-700 px-3 py-1 font-mono text-xs uppercase tracking-wider text-zinc-200 hover:bg-zinc-800">
          retry
        </button>
      ) : null}
      {state === 'linked' ? (
        <button type="button" onClick={onContinue} className="w-fit rounded border border-emerald-500/50 px-4 py-2 font-mono text-xs uppercase tracking-wider text-emerald-300 hover:bg-emerald-500/10">
          events flowing — review posture →
        </button>
      ) : null}
    </section>
  );
}
