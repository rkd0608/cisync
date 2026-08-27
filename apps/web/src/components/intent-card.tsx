'use client';

import Link from 'next/link';
import type { ReactElement } from 'react';
import { IdLabel } from './id-label';
import { RiskPill } from './risk-pill';
import { StateBadge } from './state-badge';
import type { BoardIntent } from '@/lib/event-board';
import { formatCountdown } from '@/lib/format';
import type { EventEnvelope } from '@/lib/event-schemas';

const COUNTDOWN_TONES: Record<string, string> = {
  overdue: 'text-[var(--color-risk-critical)]',
  soon: 'text-[var(--color-risk-medium)]',
  calm: 'text-zinc-500',
  none: 'text-zinc-600',
};

function utcClock(iso: string): string {
  const parsed = Date.parse(iso);
  return Number.isNaN(parsed) ? '--:--:--' : new Date(parsed).toISOString().slice(11, 19);
}

// L0→L1 disclosure on hover (§7 depth rules): the card IS the summary; the
// popover (pure CSS group-hover — no portal, no dep) previews this intent's
// last ledger events. Escape hatch: keyboard focus shows it too.
export function IntentCard({
  intent,
  events,
}: {
  intent: BoardIntent;
  events: EventEnvelope[];
}): ReactElement {
  const countdown = formatCountdown(intent.deadline, Date.now());
  return (
    <Link
      href={`/intents/${intent.id}`}
      className="card-glass card-hover group relative block px-4 py-3"
      data-testid={`intent-card-${intent.id}`}
    >
      <div className="flex flex-wrap items-center gap-2">
        <StateBadge state={intent.state} />
        <RiskPill risk={intent.riskClass} />
        <span className={`font-mono text-[11px] ${COUNTDOWN_TONES[countdown.tone] ?? ''}`}>
          {countdown.label}
        </span>
        <span className="ml-auto">
          <IdLabel id={intent.id} head={8} tail={4} />
        </span>
      </div>
      <p className="mt-1.5 truncate text-sm text-zinc-200">{intent.goal}</p>

      {events.length > 0 ? (
        <span
          tabIndex={0}
          aria-label={`recent ledger activity for ${intent.goal}`}
          className="pointer-events-none absolute left-2 top-full z-30 mt-1 hidden w-80 rounded-lg border border-white/10 bg-[var(--color-surface-raised)] p-3 shadow-[0_16px_40px_rgba(0,0,0,0.55)] group-hover:block group-focus-within:block"
        >
          <span className="section-label">mini timeline · L1</span>
          <ul className="mt-1.5 flex flex-col gap-0.5 font-mono text-[11px]">
            {events.map((event) => (
              <li key={event.id} className="flex items-baseline gap-2">
                <span className="tabular-nums text-zinc-600">{utcClock(event.occurred_at)}</span>
                <span className="truncate text-[var(--color-accent-soft)]">{event.type}</span>
              </li>
            ))}
          </ul>
          {events.length >= 5 ? (
            <span className="mt-1 block font-mono text-[10px] text-zinc-600">
              newest 5 of intent activity — full trail inside
            </span>
          ) : null}
        </span>
      ) : null}
    </Link>
  );
}

export function recentEventsFor(boardEvents: EventEnvelope[], intentId: string): EventEnvelope[] {
  return boardEvents.filter((event) => event.aggregate?.id === intentId).slice(0, 5);
}
