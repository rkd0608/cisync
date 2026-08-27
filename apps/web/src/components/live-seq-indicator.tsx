'use client';

import type { ReactElement } from 'react';

// Live-feed heartbeat (mission Part 2 micro-interaction): keyed on seq, so a
// new ledger arrival remounts the dot and replays a single pop animation —
// constant looping would bury the signal it exists to send. §7: ordering
// claims cite seq, never wall clock.
export function LiveSeqIndicator({ lastSeq }: { lastSeq: number }): ReactElement {
  return (
    <span
      data-testid="live-seq"
      className="inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-widest text-zinc-500"
      title={`ledger tail holds events up to seq ${lastSeq}`}
    >
      <span key={lastSeq} aria-hidden className={`seq-flash inline-block h-1.5 w-1.5 rounded-full ${lastSeq > 0 ? 'bg-emerald-400' : 'bg-zinc-700'}`} />
      {lastSeq > 0 ? `live · seq ${lastSeq.toLocaleString('en-US')}` : 'awaiting first event'}
    </span>
  );
}
