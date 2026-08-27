import Link from 'next/link';
import type { ReactElement } from 'react';

export function LandingFooter(): ReactElement {
  return (
    <footer className="border-t border-white/5" data-testid="landing-footer">
      <div className="mx-auto flex w-full max-w-6xl flex-col items-center justify-between gap-3 px-5 py-8 font-mono text-[11px] text-zinc-400 sm:flex-row">
        <p className="flex items-center gap-2 uppercase tracking-widest">
          <span aria-hidden className="inline-block h-1.5 w-1.5 rounded-full bg-cyan-400/70" />
          cisync · change control for agent-authored code
        </p>
        <nav className="flex gap-5 uppercase tracking-widest">
          <Link href="/login" className="hover:text-cyan-300">
            sign in
          </Link>
          <Link href="/app/setup" className="hover:text-cyan-300">
            connect a repo
          </Link>
        </nav>
      </div>
    </footer>
  );
}
