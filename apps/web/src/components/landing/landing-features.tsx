import type { ReactElement } from 'react';

interface Feature {
  glyph: string;
  title: string;
  body: string;
}

const FEATURES: Feature[] = [
  {
    glyph: '▣',
    title: 'Evidence dossiers',
    body: 'Every decision ships an immutable dossier: accepted evidence, deferred evidence with reasons, known uncertainty, post-merge obligations.',
  },
  {
    glyph: '⇶',
    title: 'Priority scheduler',
    body: 'Agent work is scheduled by risk and budget — leases, cpu-minute budgets and WIP caps keep fleets inside policy instead of stampeding CI.',
  },
  {
    glyph: '⟳',
    title: 'Bounded repair',
    body: 'Failures route to scoped repair attempts with explicit envelopes and repro commands — capped, auditable, and escalated to humans when confidence drops.',
  },
  {
    glyph: '⛓',
    title: 'Tamper-evident ledger',
    body: 'Hash-chained event log under every card and verdict. Chain verification is continuous; a broken chain fails the UI closed.',
  },
];

export function LandingFeatures(): ReactElement {
  return (
    <section className="mx-auto w-full max-w-6xl py-16" data-testid="feature-grid">
      <h2 className="text-center font-mono text-xs uppercase tracking-[0.3em] text-zinc-500">
        built for calibrated trust
      </h2>
      <div className="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {FEATURES.map((feature) => (
          <article key={feature.title} className="card-glass flex flex-col gap-2 p-6">
            <span aria-hidden className="font-mono text-lg text-cyan-400">
              {feature.glyph}
            </span>
            <h3 className="font-mono text-sm uppercase tracking-wider text-zinc-100">{feature.title}</h3>
            <p className="text-sm leading-relaxed text-zinc-400">{feature.body}</p>
          </article>
        ))}
      </div>
    </section>
  );
}
