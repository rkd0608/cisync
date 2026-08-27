import type { ReactElement } from 'react';
import Link from 'next/link';
import { LoginForm } from '@/components/login-form';
import { signupMode } from '@/lib/auth-config';

export const metadata = { title: 'Sign in · CISYNC' };

// Email+password sign-in (SPEC §3 2026-08-26). WHY the mode is resolved
// server-side: CISYNC_SIGNUP_MODE is an operator policy, not a client toggle —
// reading it here means the deployed posture decides what the UI offers,
// regardless of what the browser does with devtools.
export default function LoginPage(): ReactElement {
  const mode = signupMode();
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
          <LoginForm mode={mode} />
        </div>
        <p className="mt-6 text-center font-mono text-[10px] uppercase tracking-widest text-zinc-400">
          sessions are httpOnly cookies · verified by control-plane
        </p>
      </div>
    </div>
  );
}
