import type { ReactElement } from 'react';

const STATE_STYLES: Record<string, string> = {
  exploring: 'border-sky-500/40 bg-sky-500/10 text-sky-300',
  validating: 'border-cyan-400/40 bg-cyan-400/10 text-cyan-300',
  blocked: 'border-orange-500/40 bg-orange-500/10 text-orange-300',
  repairing: 'border-yellow-500/40 bg-yellow-500/10 text-yellow-200',
  merge_ready: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-300',
  eligible: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-300',
  deploying: 'border-violet-500/40 bg-violet-500/10 text-violet-300',
  monitoring: 'border-teal-500/40 bg-teal-500/10 text-teal-300',
  completed: 'border-emerald-600/50 bg-emerald-600/15 text-emerald-200',
  rejected: 'border-red-500/40 bg-red-500/10 text-red-300',
};

export function StateBadge({ state }: { state: string }): ReactElement {
  const style = STATE_STYLES[state] ?? 'border-zinc-500/40 bg-zinc-500/10 text-zinc-300';
  return (
    <span
      data-state={state}
      className={`inline-flex items-center rounded border px-1.5 py-0.5 font-mono text-[11px] uppercase tracking-wider ${style}`}
    >
      {state.replace(/_/g, ' ')}
    </span>
  );
}
