import type { ReactElement } from 'react';

// Intent state machine ladder per DOMAIN_MODEL_DRAFT §1.1:
// exploring -> validating -> blocked <-> repairing -> merge_ready ->
// deploying -> monitoring -> completed, with rejected as the terminal aside.
const LADDER = [
  'exploring',
  'validating',
  'blocked',
  'repairing',
  'merge_ready',
  'deploying',
  'monitoring',
  'completed',
] as const;

const LOOP_INDEX = 2; // blocked <-> repairing loop lives at this rung

function nodeTone(current: boolean, reached: boolean): string {
  if (current) return 'border-[var(--color-accent)] bg-[var(--color-accent)]/20 text-[var(--color-accent-soft)]';
  if (reached) return 'border-zinc-600 bg-zinc-800/60 text-zinc-300';
  return 'border-transparent text-zinc-400';
}

// Vertical cockpit rail (mission Part 3): animated pulse ring on the CURRENT
// node only — motion marks where work actually is, never decorates history.
export function StateLadderVertical({ state }: { state: string }): ReactElement {
  const currentIndex = LADDER.indexOf(state as (typeof LADDER)[number]);
  const isRejected = state === 'rejected';
  return (
    <nav aria-label="intent state ladder" data-testid="state-ladder-vertical">
      <ol className="relative flex flex-col gap-0 border-l border-zinc-800 pl-4 font-mono text-[11px]">
        {LADDER.map((step, index) => {
          const current = !isRejected && currentIndex === index;
          const reached = !isRejected && currentIndex > index;
          return (
            <li key={step} className="relative py-2 first:pt-1 last:pb-1" data-step={step} data-current={current || undefined} data-reached={reached || undefined}>
              {/* Connector dot sits on the rail line itself. */}
              <span
                aria-hidden
                className={`absolute -left-[21px] top-1/2 h-2.5 w-2.5 -translate-y-1/2 rounded-full border ${
                  current ? 'border-[var(--color-accent)] bg-[var(--color-accent)]' : reached ? 'border-zinc-500 bg-zinc-400' : 'border-zinc-700 bg-[var(--color-canvas)]'
                }`}
              >
                {current ? <span className="absolute inset-[-4px] animate-ping rounded-full bg-[var(--color-accent)] opacity-40" /> : null}
              </span>
              <span className={`rounded-md border px-1.5 py-0.5 uppercase tracking-wider ${nodeTone(current, reached)}`}>
                {step}
              </span>
              {index === LOOP_INDEX ? (
                <span aria-hidden className="ml-2 select-none text-[10px] text-zinc-400">⇄ repairing</span>
              ) : null}
            </li>
          );
        })}
        {isRejected ? (
          <li className="relative py-2 first:pt-1 last:pb-1" data-step="rejected" data-current>
            <span aria-hidden className="absolute -left-[21px] top-1/2 h-2.5 w-2.5 -translate-y-1/2 rounded-full border border-rose-500 bg-rose-500" />
            <span className="rounded-md border border-rose-500/60 px-1.5 py-0.5 uppercase tracking-wider text-rose-300">rejected</span>
          </li>
        ) : null}
      </ol>
    </nav>
  );
}
