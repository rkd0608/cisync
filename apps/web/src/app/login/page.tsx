import type { ReactElement } from 'react';
import Link from 'next/link';
import { LoginForm } from '@/components/login-form';

export const metadata = { title: 'Sign in · CISYNC' };

// Passwordless sign-in: email → 6-digit code. The form is a client island;
// this page stays a server component so metadata and static shell render fast.
export default function LoginPage(): ReactElement {
  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center px-5 py-12">
      {/* Brand glow mirrors the landing hero (coherent transition surfaces). */}
      <div aria-hidden className="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_top,rgba(129,140,248,0.1),transparent_55%)]" />
      <div className="relative w-full max-w-sm">
        <Link href="/" className="mb-8 flex items-center justify-center gap-2 font-mono text-sm tracking-widest text-zinc-100">
          <span aria-hidden className="inline-block h-2 w-2 animate-pulse rounded-full bg-[var(--color-accent)]" />
          CISYNC
        </Link>
        <div className="card-glass p-6 sm:p-8">
          <LoginForm />
        </div>
        <p className="mt-6 text-center font-mono text-[10px] uppercase tracking-widest text-zinc-700">
          access is allowlisted · codes expire in 10 minutes
        </p>
      </div>
    </div>
  );
}
