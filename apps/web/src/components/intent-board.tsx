'use client';

import Link from 'next/link';
import { BoardSummaryStrip } from './board-summary-strip';
import { EmptyState } from './empty-state';
import { ErrorState, type ApiErrorView } from './error-state';
import { EventTimeline } from './event-timeline';
import { IntentCard, recentEventsFor } from './intent-card';
import { LiveSeqIndicator } from './live-seq-indicator';
import { RelationBadge } from './relation-badge';
import { SkeletonRows } from './skeleton';
import { StateBadge } from './state-badge';
import { deriveSummary, type BoardCandidate, type BoardIntent, type BoardState } from '@/lib/event-board';
import { truncateMiddle } from '@/lib/format';
import type { EventEnvelope } from '@/lib/event-schemas';
import { groupIntents, type BoardGroupMode } from '@/lib/board-filters';

export interface IntentBoardProps {
  phase: 'loading' | 'ready' | 'error';
  board: BoardState;
  visibleIntents: BoardIntent[];
  visibleCandidates: Array<{ candidate: BoardCandidate; parent: BoardIntent | undefined }>;
  errorMessage: string | null;
  retry: () => void;
  groupBy: BoardGroupMode;
}

// Command-center board (mission Part 3): KPI strip up top, swimlanes grouped
// by state|risk, ledger tail aside. Swimlanes scroll horizontally at density
// instead of stacking into an ever-lengthening page. The board renders only
// after the client poll loop resolves, so reading the clock here is safe
// (no SSR/hydration divergence).
export function IntentBoard(props: IntentBoardProps): React.ReactElement {
  const summary = deriveSummary(props.board, new Date().toISOString());
  return (
    <div className="flex flex-col gap-6">
      <BoardSummaryStrip summary={summary} lastSeq={props.board.lastSeq} />
      <div className="grid gap-6 xl:grid-cols-[1fr_320px]">
        <section className="flex min-w-0 flex-col gap-3">
          <h2 className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">
            change intents · swimlanes by {props.groupBy}
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
              action={{ label: 'connect a repo at /app/setup', href: '/app/setup' }}
            />
          ) : (
            <Swimlanes intents={props.visibleIntents} groupBy={props.groupBy} timeline={props.board.timeline} />
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

        <aside className="flex min-w-0 flex-col gap-3">
          <div className="flex items-center justify-between gap-2">
            <h2 className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">ledger tail</h2>
            <LiveSeqIndicator lastSeq={props.board.lastSeq} />
          </div>
          <div className="card-glass p-2">
            <EventTimeline events={props.board.timeline.slice(0, 30) as EventEnvelope[]} />
          </div>
        </aside>
      </div>
    </div>
  );
}

function Swimlanes({
  intents,
  groupBy,
  timeline,
}: {
  intents: BoardIntent[];
  groupBy: BoardGroupMode;
  timeline: EventEnvelope[];
}): React.ReactElement {
  const sections = groupIntents(intents, groupBy);
  return (
    <div className="-mx-1 flex snap-x gap-4 overflow-x-auto px-1 pb-2" data-testid={`board-${groupBy}-swimlanes`}>
      {sections.map((section) => (
        <div
          key={section.key}
          data-testid={`group-${section.key}`}
          className="flex w-72 shrink-0 snap-start flex-col gap-2 rounded-lg border border-white/5 bg-white/[0.015] p-3"
        >
          <p className="font-mono text-[10px] uppercase tracking-[0.25em] text-zinc-500">
            ── {section.key.replace(/_/g, ' ')} · <span className="tabular-nums">{section.items.length}</span>
          </p>
          <ul className="flex flex-col gap-2">
            {section.items.map((intent) => (
              <li key={intent.id}>
                <IntentCard intent={intent} events={recentEventsFor(timeline, intent.id)} />
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
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
        <tr className="border-b border-zinc-800 text-left text-[10px] uppercase tracking-widest text-zinc-400">
          <th className="py-1.5 pr-2">state</th>
          <th className="py-1.5 pr-2">head sha</th>
          <th className="py-1.5 pr-2">cluster</th>
          <th className="py-1.5">relation</th>
        </tr>
      </thead>
      <tbody>
        {rows.map(({ candidate }) => (
          <tr key={candidate.id} className="border-b border-zinc-900 transition-colors hover:bg-white/[0.03] last:border-0">
            <td className="py-1.5 pr-2">
              <Link href={`/candidates/${candidate.id}`} data-testid={`cand-link-${candidate.id}`} className="hover:text-[var(--color-accent-soft)]">
                <StateBadge state={candidate.state} />
              </Link>
            </td>
            <td className="py-1.5 pr-2">{candidate.headSha ? truncateMiddle(candidate.headSha, 8, 4) : '--'}</td>
            <td className="py-1.5 pr-2 text-zinc-400">
              {candidate.clusterId ? (
                <Link href={`/clusters/${candidate.clusterId}`} className="hover:text-cyan-300" title={candidate.clusterId}>
                  {truncateMiddle(candidate.clusterId, 8, 4)}
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
