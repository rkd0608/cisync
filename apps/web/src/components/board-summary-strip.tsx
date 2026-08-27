import type { ReactElement } from 'react';
import type { BoardSummary } from '@/lib/event-board';

// Command-center KPI tiles (mission Part 3): active intents · decisions
// today · validations in-flight. "today" counts the loaded tail only — the
// tooltip says so explicitly rather than implying a historical scan. The
// malformed tile is deliberate honesty: silently dropping bad events would
// hide upstream contract drift.
export function BoardSummaryStrip({
  summary,
  lastSeq,
}: {
  summary: BoardSummary;
  lastSeq?: number;
}): ReactElement {
  void lastSeq; // seq pulse lives on the tail panel; kept off the KPI row
  const tiles = [
    { label: 'active intents', value: summary.activeIntents, tone: 'text-[var(--color-accent-soft)]', note: 'exploring · validating · blocked · repairing' },
    { label: 'merge ready', value: summary.mergeReadyIntents, tone: 'text-emerald-300', note: 'eligible verdict reached; awaiting merge train' },
    { label: 'validating in flight', value: summary.inFlightCandidates, tone: 'text-sky-300', note: 'candidates submitted → resolving' },
    { label: 'decisions today (UTC)', value: summary.decisionsToday, tone: 'text-violet-300', note: `rendered today; ${summary.decisionsRendered} lifetime from loaded tail` },
    {
      label: 'malformed events',
      value: summary.malformedEvents,
      tone: summary.malformedEvents > 0 ? 'text-[var(--color-risk-critical)]' : 'text-zinc-500',
      note: summary.malformedEvents > 0 ? 'upstream payload drift — investigate' : 'ledger payloads all conform',
    },
  ];
  return (
    <div
      data-testid="board-summary"
      className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-white/8 bg-white/8 sm:grid-cols-3 lg:grid-cols-5"
    >
      {tiles.map((tile) => (
        <div key={tile.label} className="bg-[var(--color-surface)] px-4 py-3 transition-colors hover:bg-[var(--color-surface-raised)]" title={tile.note}>
          <p className={`font-mono text-xl tabular-nums ${tile.tone}`}>{tile.value}</p>
          <p className="font-mono text-[10px] uppercase tracking-widest text-zinc-600">{tile.label}</p>
        </div>
      ))}
    </div>
  );
}
