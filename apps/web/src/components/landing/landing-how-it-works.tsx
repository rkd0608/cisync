import type { ReactElement } from 'react';

const STEPS: Array<{ index: string; title: string; body: string }> = [
  {
    index: '01',
    title: 'Install App',
    body: 'Add the CISync GitHub App to the repos you want governed. Read-only metadata and diffs — never secrets, never source storage.',
  },
  {
    index: '02',
    title: 'Open PR',
    body: 'Developers and agents open pull requests exactly as today. Webhooks land in the ingest pipe within seconds.',
  },
  {
    index: '03',
    title: 'Verified check',
    body: 'The Agent Verification Gate runs: evidence is collected, a decision renders with confidence, and the check explains what ran — and what was skipped, and why.',
  },
];

export function LandingHowItWorks(): ReactElement {
  return (
    <section className="mx-auto w-full max-w-6xl py-16" data-testid="how-it-works">
      <h2 className="text-center font-mono text-xs uppercase tracking-[0.3em] text-zinc-500">
        how it works
      </h2>
      <ol className="mt-10 grid gap-4 md:grid-cols-3">
        {STEPS.map((step) => (
          <li key={step.index} className="card-glass p-6">
            <p className="font-mono text-xs tracking-widest text-cyan-400">{step.index}</p>
            <h3 className="mt-2 font-mono text-sm uppercase tracking-wider text-zinc-100">{step.title}</h3>
            <p className="mt-2 text-sm leading-relaxed text-zinc-400">{step.body}</p>
          </li>
        ))}
      </ol>
    </section>
  );
}
