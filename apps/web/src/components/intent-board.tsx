'use client';

import Link from 'next/link';
import { BoardSummaryStrip } from './board-summary-strip';
import { EmptyState } from './empty-state';
import { ErrorState, type ApiErrorView } from './error-state';
import { EventTimeline } from './event-timeline';
import { RelationBadge } from './relation-badge';
import { RiskPill } from './risk-pill';
import { StateBadge } from './state-badge';
import {
  deriveSummary,
  type BoardCandidate,
  type BoardIntent,
  type BoardState,
} from '@/lib/event-board';
import { formatCountdown, shortSha } from '@/lib/format';
import type { EventEnvelope } from '@/lib/event-schemas';
import { groupIntents } from '@/lib/board-filters';
import type { BoardGroupMode } from '@/lib/board-filters';

const COUNTDOWN_TONES: Record<string, string> = {
  overdue: 'text-red-400',
  soon: 'text-amber-300',
  calm: 'text-zinc-500',
  none: 'text-zinc-600',
};

export interface IntentBoardProps {
  phase: 'loading' | 'ready' | 'error';
  board: BoardState;
  visibleIntents: BoardIntent[];
  visibleCandidates: Array<{ candidate: BoardCandidate; parent: BoardIntent | undefined }>;
  errorMessage: string | null;
  retry: () => void;
  groupBy: BoardGroupMode;
}

export function IntentBoard(props: IntentBoardProps): React.ReactElement {
  const summary = deriveSummary(props.board);
  return (
    <div className="flex flex-col gap-6">
      <BoardSummaryStrip summary={summary} />
      <div className="grid gap-6 lg:grid-cols-[1fr_320px]">
        <section className="flex flex-col gap-3">
          <h2 className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">
            change intents · ledger-derived
          </h2>
          {props.phase === 'loading' ? (
            <SkeletonRows rows={4} />
          ) : props.phase === 'error' ? (
            <ErrorState
              error={{ code: 'sync_failed', message: props.errorMessage ?? 'ledger sync failed' } satisfies ApiErrorView}
              onRetry={props.retry}
            />
          ) : props.visibleIntents.length === 0 ? (
            <EmptyState
              what="no change activity yet"
              whyEmpty="Open a PR on a connected repo, or POST /v1/change-intents. Intents appear here as intent.declared events land in the ledger."
              action={{ label: 'connect a repo at /onboarding', href: '/onboarding' }}
            />
          ) : (
            <IntentSections intents={props.visibleIntents} groupBy={props.groupBy} />
          )}

          <h2 className="mt-4 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
            candidates · {props.visibleCandidates.length}
          </h2>
          {props.phase === 'loading' ? (
            <SkeletonRows rows={3} />
          ) : props.visibleCandidates.length === 0 ? (
            <EmptyState
              what="no candidates in view"
              whyEmpty="Candidates appear when agents submit work against a declared intent (candidate.submitted events)."
            />
          ) : (
            <CandidateTable rows={props.visibleCandidates} />
          )}
        </section>

        <aside className="flex flex-col gap-3">
          <h2 className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">
            ledger tail
          </h2>
          <div className="rounded border border-zinc-800 bg-zinc-950 p-2">
            <EventTimeline events={props.board.timeline.slice(0, 30) as EventEnvelope[]} />
          </div>
        </aside>
      </div>
    </div>
  );
}

function IntentSections({
  intents,
  groupBy,
}: {
  intents: BoardIntent[];
  groupBy: BoardGroupMode;
}): React.ReactElement {
  const sections = groupIntents(intents, groupBy);
  return (
    <>
      {sections.map((section) => (
        <div key={section.key} data-testid={`group-${section.key}`} className="flex flex-col gap-2">
          <p className="mt-1 font-mono text-[10px] uppercase tracking-[0.25em] text-zinc-600">
            ── {section.key.replace(/_/g, ' ')} · {section.items.length}
          </p>
          <ul className="flex flex-col gap-2">
            {section.items.map((intent) => (
              <li key={intent.id}>
                <IntentCard intent={intent} />
              </li>
            ))}
          </ul>
        </div>
      ))}
    </>
  );
}

function IntentCard({ intent }: { intent: BoardIntent }): React.ReactElement {
  const countdown = formatCountdown(intent.deadline, Date.now());
  return (
    <Link
      href={`/intents/${intent.id}`}
      className="block rounded border border-zinc-800 bg-zinc-950 px-4 py-3 hover:border-zinc-600"
    >
      <div className="flex flex-wrap items-center gap-2">
        <StateBadge state={intent.state} />
        <RiskPill risk={intent.riskClass} />
        <span className={`font-mono text-[11px] ${COUNTDOWN_TONES[countdown.tone] ?? ''}`}>
          {countdown.label}
        </span>
        <span className="ml-auto font-mono text-[11px] text-zinc-600">{shortSha(intent.id)}</span>
      </div>
      <p className="mt-1.5 truncate text-sm text-zinc-200">{intent.goal}</p>
    </Link>
  );
}

function CandidateTable({
  rows,
}: {
  rows: Array<{ candidate: BoardCandidate; parent: BoardIntent | undefined }>;
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
        {rows.map(({ candidate }) => (
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
        <div key={index} className="h-14 animate-pulse rounded border border-zinc-900 bg-zinc-900/50" />
      ))}
    </div>
  );
}
