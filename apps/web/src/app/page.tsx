import Link from 'next/link';
import type { ReactElement } from 'react';
import { LandingFeatures } from '@/components/landing/landing-features';
import { LandingFooter } from '@/components/landing/landing-footer';
import { LandingHero } from '@/components/landing/landing-hero';
import { LandingHowItWorks } from '@/components/landing/landing-how-it-works';
import { LandingSocialProof } from '@/components/landing/landing-social-proof';

export default function LandingPage(): ReactElement {
  return (
    <div className="relative flex min-h-screen flex-col">
      <header className="absolute inset-x-0 top-0 z-20">
        <div className="mx-auto flex w-full max-w-6xl items-center gap-4 px-5 py-4 font-mono text-sm">
          <Link href="/" className="flex items-center gap-2 tracking-widest text-zinc-100">
            <span aria-hidden className="inline-block h-2 w-2 animate-pulse rounded-full bg-[var(--color-accent)]" />
            CISYNC
          </Link>
          <Link href="/login" className="ml-auto text-xs text-zinc-400 transition-colors hover:text-[var(--color-accent-soft)]">
            sign in →
          </Link>
        </div>
      </header>
      <div className="flex-1">
        <LandingHero />
        <LandingSocialProof />
        <LandingHowItWorks />
        <LandingFeatures />
        {/* §7 discipline: the console itself is the demo — point builders at it. */}
        <section className="mx-auto w-full max-w-3xl px-5 pb-8 text-center">
          <p className="text-xs leading-relaxed text-zinc-500">
            Shadow mode first: decisions are recorded and scored against real CI outcomes before anything
            is enforced. Trust is earned with evidence, not asserted.
          </p>
        </section>
      </div>
      <LandingFooter />
    </div>
  );
}
