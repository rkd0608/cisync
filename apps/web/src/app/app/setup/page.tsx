import type { ReactElement } from 'react';
import Link from 'next/link';
import { SetupFlow } from '@/components/setup/setup-flow';

export const metadata = {
  title: 'Set up CISync · first-run',
};

// Guided FIRST-RUN flow (mission Part 1): connect GitHub → watch the first
// verification land → review posture. All backend calls happen client-side so
// endpoint absence degrades to honest awaiting-backend states. Already-
// completed tenants are detected post-mount and routed around the flow.
export default function AppSetupPage(): ReactElement {
  return (
    <div className="route-rise mx-auto flex w-full max-w-3xl flex-col gap-6 px-5 py-10">
      <header className="flex flex-col gap-2">
        <h1 className="font-mono text-lg tracking-wide text-zinc-100">set up cisync</h1>
        <p className="max-w-2xl text-sm leading-relaxed text-zinc-500">
          Three steps: install the GitHub App, prove one verification arrives
          end-to-end, then review the compiled-in posture. Nothing is enforced
          during any of this — shadow mode only. Progress is kept locally if
          you need to leave:{' '}
          <Link href="/dashboard" className="text-cyan-400 hover:text-cyan-300">
            skip to the board →
          </Link>
        </p>
      </header>
      <SetupFlow />
    </div>
  );
}
