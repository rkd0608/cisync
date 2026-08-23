import type { ReactElement } from 'react';
import type { BoardSummary } from '@/lib/event-board';

// Queue/capacity strip derived from the ledger projection. The malformed tile
// is deliberate honesty: silently dropping bad events would hide upstream
// contract drift.
export function BoardSummaryStrip({ summary }: { summary: BoardSummary }): ReactElement {
  const tiles = [
    { label: 'active intents', value: summary.activeIntents, tone: 'text-cyan-300' },
    { label: 'merge ready', value: summary.mergeReadyIntents, tone: 'text-emerald-300' },
    { label: 'candidates in flight', value: summary.inFlightCandidates, tone: 'text-sky-300' },
    { label: 'eligible', value: summary.eligibleCandidates, tone: 'text-emerald-300' },
    { label: 'decisions rendered', value: summary.decisionsRendered, tone: 'text-violet-300' },
    {
      label: 'malformed events',
      value: summary.malformedEvents,
      tone: summary.malformedEvents > 0 ? 'text-red-400' : 'text-zinc-500',
    },
  ];
  return (
    <div
      data-testid="board-summary"
      className="grid grid-cols-2 gap-px overflow-hidden rounded border border-zinc-800 bg-zinc-800 sm:grid-cols-3 lg:grid-cols-6"
    >
      {tiles.map((tile) => (
        <div key={tile.label} className="bg-zinc-950 px-4 py-3">
          <p className={`font-mono text-xl tabular-nums ${tile.tone}`}>{tile.value}</p>
          <p className="font-mono text-[10px] uppercase tracking-widest text-zinc-600">
            {tile.label}
          </p>
        </div>
      ))}
    </div>
  );
}
