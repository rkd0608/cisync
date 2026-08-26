import type { Metadata } from 'next';
import Link from 'next/link';
import './globals.css';

export const metadata: Metadata = {
  title: 'CISYNC · change control',
  description: 'Air traffic control for code changes — intents, candidates, evidence.',
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>): React.ReactElement {
  return (
    <html lang="en">
      <body className="min-h-screen antialiased">
        <header className="border-b border-zinc-800 bg-black/40 backdrop-blur">
          <div className="mx-auto flex max-w-6xl items-center gap-4 px-5 py-3 font-mono text-sm">
            <Link href="/" className="flex items-center gap-2 tracking-widest text-zinc-100">
              <span aria-hidden className="inline-block h-2 w-2 animate-pulse rounded-full bg-cyan-400" />
              CISYNC
            </Link>
            <span className="text-[10px] uppercase tracking-[0.25em] text-zinc-600">
              change control console
            </span>
            <nav className="ml-auto flex gap-4 text-xs text-zinc-400">
              <Link href="/" className="hover:text-cyan-300">
                board
              </Link>
              <Link href="/installations" className="hover:text-cyan-300">
                installations
              </Link>
              <Link href="/onboarding" className="hover:text-cyan-300">
                onboarding
              </Link>
            </nav>
          </div>
        </header>
        <main className="mx-auto max-w-6xl px-5 py-8">{children}</main>
        <footer className="border-t border-zinc-900 py-3 text-center font-mono text-[10px] uppercase tracking-widest text-zinc-700">
          read-mostly · every number derived from the ledger · nothing fabricated
        </footer>
      </body>
    </html>
  );
}
