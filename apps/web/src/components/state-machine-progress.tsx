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

export function StateMachineProgress({ state }: { state: string }): ReactElement {
  const currentIndex = LADDER.indexOf(state as (typeof LADDER)[number]);
  const isRejected = state === 'rejected';
  return (
    <div className="flex flex-wrap items-center gap-1 font-mono text-[11px]">
      {LADDER.map((step, index) => {
        const reached = !isRejected && currentIndex >= index;
        const current = !isRejected && currentIndex === index;
        return (
          <span key={step} className="flex items-center gap-1">
            {index > 0 ? (
              <span
                aria-hidden
                className={reached ? 'text-zinc-500' : 'text-zinc-800'}
                title={index === 3 ? 'blocked <-> repairing loop' : undefined}
              >
                {index === 2 ? '<->' : '->'}
              </span>
            ) : null}
            <span
              data-step={step}
              data-current={current || undefined}
              className={[
                'rounded px-1.5 py-0.5 uppercase tracking-wider border',
                current
                  ? 'border-cyan-400 bg-cyan-400/15 text-cyan-200'
                  : reached
                    ? 'border-zinc-700 text-zinc-300'
                    : 'border-transparent text-zinc-600',
              ].join(' ')}
            >
              {step}
            </span>
          </span>
        );
      })}
      {isRejected ? (
        <>
          <span aria-hidden className="text-zinc-800">-&gt;</span>
          <span
            data-step="rejected"
            data-current
            className="rounded border border-red-500 bg-red-500/15 px-1.5 py-0.5 uppercase tracking-wider text-red-200"
          >
            rejected
          </span>
        </>
      ) : null}
    </div>
  );
}
