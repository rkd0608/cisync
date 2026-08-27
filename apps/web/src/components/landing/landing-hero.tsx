import Link from 'next/link';
import type { ReactElement } from 'react';
import { githubAppInstallUrl } from '@/lib/auth-config';

// Cinematic hero (mission Part 3): display face reserved to marketing
// surfaces, layered indigo/cyan glow, progressive disclosure into the how-
// it-works band below the fold.
export function LandingHero(): ReactElement {
  const installUrl = githubAppInstallUrl();
  return (
    <section className="relative flex flex-col items-center gap-7 py-28 text-center sm:py-36">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 -z-10 h-[520px] bg-[radial-gradient(ellipse_55%_45%_at_50%_-5%,rgba(129,140,248,0.16),transparent_65%),radial-gradient(ellipse_35%_30%_at_65%_10%,rgba(34,211,238,0.08),transparent_70%)]"
      />
      <p className="rounded-full border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/5 px-3 py-1 font-mono text-[10px] uppercase tracking-[0.3em] text-[var(--color-accent-soft)]">
        agent verification gate · v0.2
      </p>
      <h1 className="max-w-4xl text-balance font-display text-5xl font-semibold leading-[1.05] tracking-tight text-zinc-50 sm:text-6xl">
        CISync: every agent-authored change,{' '}
        <span className="bg-gradient-to-r from-[var(--color-accent-soft)] via-cyan-300 to-emerald-300 bg-clip-text text-transparent">
          verified before merge
        </span>
      </h1>
      <p className="max-w-2xl text-balance text-sm leading-relaxed text-zinc-400 sm:text-base">
        Evidence dossiers instead of green checkmarks. Priority scheduling for agent work.
        Bounded, scoped repair. A tamper-evident ledger behind every decision your agents make.
      </p>
      <div className="flex flex-col items-center gap-3 sm:flex-row">
        <Link
          href="/login"
          data-testid="cta-get-started"
          className="rounded-lg bg-gradient-to-r from-[var(--color-accent)] to-cyan-400 px-6 py-3 font-mono text-sm font-semibold text-black shadow-[0_0_36px_rgba(99,102,241,0.35)] transition-transform hover:-translate-y-0.5"
        >
          Get started →
        </Link>
        {installUrl !== null ? (
          <a
            href={installUrl}
            data-testid="cta-install-app"
            className="rounded-lg border border-white/20 px-6 py-3 font-mono text-sm text-zinc-200 backdrop-blur transition-colors hover:border-[var(--color-accent)]/60 hover:text-[var(--color-accent-soft)]"
          >
            Install GitHub App ↗
          </a>
        ) : (
          // WHY a muted note instead of a dead link: the install URL comes from
          // NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL; fabricating one would lie.
          <span
            data-testid="cta-install-app-unset"
            className="rounded-lg border border-dashed border-white/25 px-6 py-3 font-mono text-xs text-zinc-400"
          >
            GitHub App install link coming shortly — sign in meanwhile
          </span>
        )}
      </div>
    </section>
  );
}
