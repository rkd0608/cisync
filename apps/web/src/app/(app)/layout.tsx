import Link from 'next/link';
import { headers } from 'next/headers';
import type { ReactElement } from 'react';
import { HEADER_AUTH_EMAIL } from '@/middleware';
import { LogoutButton } from '@/components/logout-button';

const NAV_LINKS: Array<{ href: string; label: string }> = [
  { href: '/dashboard', label: 'board' },
  { href: '/app/setup', label: 'setup' },
  { href: '/installations', label: 'installations' },
];

// Console shell shared by every authenticated route in the group. WHY the
// identity arrives via middleware-set request header: verification happened
// ONCE at the oracle boundary (GET /v1/auth/me through the gateway, see
// middleware.ts); this layout must not grow a second verification path —
// every matched page implies a fresh oracle round-trip already ran.
export default async function AppLayout({
  children,
}: Readonly<{ children: React.ReactNode }>): Promise<ReactElement> {
  const verifiedEmail = (await headers()).get(HEADER_AUTH_EMAIL);

  return (
    <div className="flex min-h-screen flex-col">
      <header className="sticky top-0 z-20 border-b border-white/10 bg-black/50 backdrop-blur-md">
        <div className="mx-auto flex w-full max-w-7xl items-center gap-4 px-5 py-3 font-mono text-sm">
          <Link href="/dashboard" className="flex items-center gap-2 tracking-widest text-zinc-100">
            <span aria-hidden className="inline-block h-2 w-2 animate-pulse rounded-full bg-cyan-400" />
            CISYNC
          </Link>
          <span className="hidden text-[10px] uppercase tracking-[0.25em] text-zinc-400 sm:inline">
            change control console
          </span>
      <nav className="ml-auto flex items-center gap-4 text-xs text-zinc-400">
        {NAV_LINKS.map((link) => (
          <Link
            key={link.href}
            href={link.href}
            className="transition-colors hover:text-[var(--color-accent-soft)]"
          >
            {link.label}
          </Link>
        ))}
      </nav>
          <div className="flex items-center gap-3 border-l border-white/10 pl-4 text-xs">
            {verifiedEmail !== null ? (
              <>
                <span className="hidden max-w-[16rem] truncate text-zinc-400 md:inline" title={verifiedEmail}>
                  {verifiedEmail}
                </span>
                <LogoutButton />
              </>
            ) : (
              <Link href="/login" className="text-cyan-300 hover:text-cyan-200">
                sign in
              </Link>
            )}
          </div>
        </div>
      </header>
      {/* route-rise gives a minimal enter transition (mission Part 2
          micro-interactions) without any router-level dependency. */}
      <main className="route-rise mx-auto w-full max-w-7xl flex-1 px-5 py-8">{children}</main>
      <footer className="border-t border-white/5 py-3 text-center font-mono text-[10px] uppercase tracking-widest text-zinc-500">
        read-mostly · every number derived from the ledger · nothing fabricated
      </footer>
    </div>
  );
}
