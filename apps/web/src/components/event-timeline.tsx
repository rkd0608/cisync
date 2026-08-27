import type { ReactElement } from 'react';
import type { EventEnvelope } from '@/lib/event-schemas';

const ACTOR_GLYPHS: Record<string, string> = {
  agent: 'ag',
  human: 'hu',
  service: 'sv',
  github: 'gh',
};

function utcClock(iso: string): string {
  const parsed = Date.parse(iso);
  if (Number.isNaN(parsed)) return '--:--:--';
  return new Date(parsed).toISOString().slice(11, 19);
}

// Ledger tail view. Deterministic UTC clock labels keep server/client markup
// identical (no hydration mismatch).
export function EventTimeline({ events }: { events: EventEnvelope[] }): ReactElement {
  if (events.length === 0) {
    return (
      <p className="px-1 py-3 font-mono text-[11px] uppercase tracking-widest text-zinc-400">
        ledger quiet
      </p>
    );
  }
  return (
    <ul data-testid="event-timeline" className="flex flex-col gap-1 font-mono text-[11px]">
      {events.map((event) => {
        const actorKind = event.actor?.kind ?? 'service';
        return (
          <li
            key={event.id}
            className="flex items-baseline gap-2 rounded px-2 py-1 hover:bg-zinc-900"
          >
            <span className="text-zinc-400 tabular-nums">{utcClock(event.occurred_at)}</span>
            <span className="w-6 shrink-0 text-zinc-500">{ACTOR_GLYPHS[actorKind] ?? '??'}</span>
            <span className="truncate text-cyan-300">{event.type}</span>
            {event.aggregate?.id ? (
              <span className="ml-auto max-w-[9rem] truncate text-zinc-400" title={event.aggregate.id}>
                {event.aggregate.id}
              </span>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}
