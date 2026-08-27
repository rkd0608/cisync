import type { ReactElement } from 'react';

// Social-proof strip (mission Part 3): an explicitly-labeled PLACEHOLDER —
// calibrating copy applies to marketing too (T4), so we do not fabricate
// customer names or metrics. Wordmark slots are monogram boxes to be filled
// by real pilot partners when they exist.
const SLOTS = ['acz', 'krn', 'vlt', 'mtr', 'os1', 'pl8'] as const;

export function LandingSocialProof(): ReactElement {
  return (
    <section className="mx-auto w-full max-w-6xl border-y border-white/5 py-10" data-testid="social-proof-strip">
      <p className="text-center font-mono text-[10px] uppercase tracking-[0.3em] text-zinc-400">
        pilot cohort forming · slots reserved for real partners · no fabricated endorsements
      </p>
      <div className="mt-6 flex flex-wrap items-center justify-center gap-x-10 gap-y-4" aria-hidden>
        {SLOTS.map((slot) => (
          <span
            key={slot}
            data-testid={`proof-slot-${slot}`}
            title="reserved for a pilot partner"
            className="flex h-9 w-24 select-none items-center justify-center rounded-md border border-dashed border-white/10 bg-white/[0.02] font-mono text-xs uppercase tracking-[0.35em] text-zinc-700 transition-colors hover:border-white/20 hover:text-zinc-600"
          >
            {slot}
          </span>
        ))}
      </div>
    </section>
  );
}
