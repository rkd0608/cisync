import type { ReactElement } from 'react';

// The §2.1 disclosure panel: set expectations honestly BEFORE install — what
// the GitHub App receives vs what it never touches. Static truth, no state.
const RECEIVES = ['repo metadata + diffs', 'CI results & timings', 'PR / check webhooks'];
const NEVER_TOUCHES = ['source storage', 'secrets', 'merges, YAML, code'];

export function DisclosurePanel(): ReactElement {
  return (
    <div data-testid="disclosure-panel" className="grid gap-4 sm:grid-cols-2">
      <section className="rounded border border-emerald-500/40 bg-emerald-500/5 px-4 py-3">
        <h3 className="font-mono text-[11px] uppercase tracking-widest text-emerald-300">
          what we receive
        </h3>
        <ul className="mt-2 flex flex-col gap-1 font-mono text-xs text-zinc-300">
          {RECEIVES.map((item) => (
            <li key={item}>✓ {item}</li>
          ))}
        </ul>
      </section>
      <section className="rounded border border-red-500/40 bg-red-500/5 px-4 py-3">
        <h3 className="font-mono text-[11px] uppercase tracking-widest text-red-300">
          what we never touch
        </h3>
        <ul className="mt-2 flex flex-col gap-1 font-mono text-xs text-zinc-300">
          {NEVER_TOUCHES.map((item) => (
            <li key={item}>✗ {item}</li>
          ))}
        </ul>
      </section>
    </div>
  );
}
