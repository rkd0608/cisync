import type { ReactElement } from 'react';

// §7 verdict/state glyphs: outline-only badges. In-flight states pulse
// instead of filling; terminal states keep their color at low saturation.
const STATE_STYLES: Record<string, string> = {
  exploring: 'border-sky-500/40 text-sky-300',
  validating: 'border-cyan-400/40 text-cyan-300 animate-pulse',
  blocked: 'border-orange-500/40 text-orange-300',
  repairing: 'border-yellow-500/40 text-yellow-200 animate-pulse',
  merge_ready: 'border-emerald-500/40 text-emerald-300',
  eligible: 'border-emerald-500/40 text-emerald-300',
  deploying: 'border-violet-500/40 text-violet-300',
  monitoring: 'border-teal-500/40 text-teal-300',
  completed: 'border-emerald-600/50 text-emerald-200',
  // Mission Part 2 verdict mapping: rejected renders rose (reserving raw red
  // for failures/security/stalled feeds per §7 red-scarcity rule).
  rejected: 'border-rose-500/40 text-rose-300',
};

export function StateBadge({ state }: { state: string }): ReactElement {
  const style = STATE_STYLES[state] ?? 'border-zinc-600 text-zinc-400';
  return (
    <span
      data-state={state}
      className={`inline-flex items-center rounded border px-1.5 py-0.5 font-mono text-[11px] uppercase tracking-wider ${style}`}
    >
      {state.replace(/_/g, ' ')}
    </span>
  );
}
