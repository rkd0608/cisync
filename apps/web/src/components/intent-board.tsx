'use client';

import Link from 'next/link';
import { useMemo } from 'react';
import { BoardSummaryStrip } from '@/components/board-summary-strip';
import { EmptyState } from '@/components/empty-state';
import { ErrorState, type ApiErrorView } from '@/components/error-state';
import { EventTimeline } from '@/components/event-timeline';
import { RelationBadge } from '@/components/relation-badge';
import { RiskPill } from '@/components/risk-pill';
import { StateBadge } from '@/components/state-badge';
import { deriveSummary, type BoardCandidate } from '@/lib/event-board';
import { formatCountdown, shortSha } from '@/lib/format';
import { useEventBoard } from '@/lib/use-event-board';

const COUNTDOWN_TONES: Record<string, string> = {
  overdue: 'text-red-400',
  soon: 'text-amber-300',
  calm: 'text-zinc-500',
  none: 'text-zinc-600',
};

export function IntentBoard(): React.ReactElement {
  const { phase, board, errorMessage, retry } = useEventBoard();
  const summary = useMemo(() => deriveSummary(board), [board]);
  const intents = Object.values(board.intents).sort(
    (a, b) => a.createdAtSeq - b.createdAtSeq,
  );
  const candidates = Object.values(board.candidates);

  if (phase === 'error') {
    const view: ApiErrorView = {
      code: 'sync_failed',
      message: errorMessage ?? 'ledger sync failed',
    };
    return <ErrorState error={view} onRetry={retry} />;
  }

  return (
    <div className="flex flex-col gap-6">
      <BoardSummaryStrip summary={summary} />
      <div className="grid gap-6 lg:grid-cols-[1fr_320px]">
        <section className="flex flex-col gap-3">
          <h2 className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">
            change intents · ledger-derived
          </h2>
          {phase === 'loading' ? (
            <SkeletonRows rows={4} />
          ) : intents.length === 0 ? (
            <EmptyState
              title="no intents on the board"
              hint="Intents appear here as intent.declared events land in the ledger."
            />
          ) : (
            <ul className="flex flex-col gap-2">
              {intents.map((intent) => {
                const countdown = formatCountdown(intent.deadline, Date.now());
                return (
                  <li key={intent.id}>
                    <Link
                      href={`/intents/${intent.id}`}
                      className="block rounded border border-zinc-800 bg-zinc-950 px-4 py-3 hover:border-zinc-600"
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <StateBadge state={intent.state} />
                        <RiskPill risk={intent.riskClass} />
                        <span
                          className={`font-mono text-[11px] ${COUNTDOWN_TONES[countdown.tone] ?? ''}`}
                        >
                          {countdown.label}
                        </span>
                        <span className="ml-auto font-mono text-[11px] text-zinc-600">
                          {intent.id}
                        </span>
                      </div>
                      <p className="mt-1.5 truncate text-sm text-zinc-200">{intent.goal}</p>
                    </Link>
                  </li>
                );
              })}
            </ul>
          )}

          <h2 className="mt-4 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
            candidates · {candidates.length}
          </h2>
          {phase === 'loading' ? (
            <SkeletonRows rows={3} />
          ) : candidates.length === 0 ? (
            <EmptyState title="no candidates submitted" />
          ) : (
            <CandidateTable candidates={candidates} />
          )}
        </section>

        <aside className="flex flex-col gap-3">
          <h2 className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">
            ledger tail
          </h2>
          <div className="rounded border border-zinc-800 bg-zinc-950 p-2">
            <EventTimeline events={board.timeline.slice(0, 30)} />
          </div>
        </aside>
      </div>
    </div>
  );
}

function CandidateTable({
  candidates,
}: {
  candidates: BoardCandidate[];
}): React.ReactElement {
  return (
    <table className="w-full border-collapse font-mono text-xs">
      <thead>
        <tr className="border-b border-zinc-800 text-left text-[10px] uppercase tracking-widest text-zinc-600">
          <th className="py-1.5 pr-2">state</th>
          <th className="py-1.5 pr-2">head sha</th>
          <th className="py-1.5 pr-2">cluster</th>
          <th className="py-1.5">relation</th>
        </tr>
      </thead>
      <tbody>
        {candidates.map((candidate) => (
          <tr key={candidate.id} className="border-b border-zinc-900 last:border-0">
            <td className="py-1.5 pr-2">
              <StateBadge state={candidate.state} />
            </td>
            <td className="py-1.5 pr-2 text-zinc-400">
              {candidate.headSha ? shortSha(candidate.headSha) : '--'}
            </td>
            <td className="py-1.5 pr-2 text-zinc-400">
              {candidate.clusterId ? (
                <Link href={`/clusters/${candidate.clusterId}`} className="hover:text-cyan-300">
                  {candidate.clusterId.slice(0, 12)}…
                </Link>
              ) : (
                '--'
              )}
            </td>
            <td className="py-1.5">
              <RelationBadge relation={candidate.relationToRep} />
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function SkeletonRows({ rows }: { rows: number }): React.ReactElement {
  return (
    <div aria-hidden className="flex flex-col gap-2" data-testid="skeleton-rows">
      {Array.from({ length: rows }, (_, index) => (
        <div
          key={index}
          className="h-14 animate-pulse rounded border border-zinc-900 bg-zinc-900/50"
        />
      ))}
    </div>
  );
}
