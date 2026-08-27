'use client';

import { useRouter } from 'next/navigation';
import { useState, type ReactElement } from 'react';

// WHY hard navigation instead of router.refresh(): the session cookie is
// httpOnly and middleware-gated, so only a full request cycle guarantees the
// protected layout re-evaluates auth state.
export function LogoutButton(): ReactElement {
  const router = useRouter();
  const [pending, setPending] = useState(false);

  async function handleLogout(): Promise<void> {
    setPending(true);
    try {
      await fetch('/api/auth/logout', { method: 'POST' });
    } finally {
      router.push('/login');
      router.refresh();
    }
  }

  return (
    <button
      type="button"
      onClick={handleLogout}
      disabled={pending}
      data-testid="logout-button"
      className="rounded border border-white/15 px-2 py-1 font-mono text-[11px] uppercase tracking-wider text-zinc-400 transition-colors hover:border-red-500/50 hover:text-red-300 disabled:opacity-50"
    >
      {pending ? '…' : 'log out'}
    </button>
  );
}
