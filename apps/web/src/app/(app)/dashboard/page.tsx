import { Suspense } from 'react';
import { BoardWorkspace } from '@/components/board-workspace';

export const dynamic = 'force-dynamic';

// Command-center surface. The header row states the operational contract in
// one line: the ledger is the only source, filters persist, nothing is
// fabricated.
export default function DashboardPage(): React.ReactElement {
  return (
    <div className="route-rise flex flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="font-mono text-lg tracking-wide text-zinc-100">live board</h1>
          <p className="mt-1 max-w-3xl text-sm text-zinc-500">
            Derived from the control-plane ledger tail — no intent-list endpoint
            exists, so this board projects events like every other consumer.
            Search and filters persist in the URL.
          </p>
        </div>
        <p className="hidden font-mono text-[10px] uppercase tracking-widest text-zinc-500 lg:block">
          dense · honest · event-sourced
        </p>
      </div>
      {/* useSearchParams requires a Suspense boundary during prerender. */}
      <Suspense fallback={<div aria-hidden className="h-12 animate-pulse rounded-lg border border-white/5 bg-white/[0.03]" />}>
        <BoardWorkspace />
      </Suspense>
    </div>
  );
}
