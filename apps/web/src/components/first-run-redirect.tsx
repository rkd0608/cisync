'use client';

import { useEffect, useState, type ReactElement } from 'react';
import { useRouter } from 'next/navigation';
import { getInstallationsStatus } from '@/lib/cisync-api';
import { isSetupComplete, windowStorageOrNull } from '@/lib/setup-storage';

// First-run interception (mission Part 1): a dashboard with zero connected
// installations has nothing to operate yet, so we send the operator to
// /app/setup exactly once per browser (skip-if-done honors the local
// tombstone). Every failure mode — endpoint down, storage blocked, unknown —
// stays put rather than trapping a working session in a loop.
export function FirstRunRedirect(): ReactElement | null {
  const router = useRouter();
  const [redirecting, setRedirecting] = useState(false);

  useEffect(() => {
    let live = true;
    const store = windowStorageOrNull();
    if (store !== null && isSetupComplete(store)) return;
    void getInstallationsStatus().then((result) => {
      if (!live || !result.ok) return;
      if (result.data.installations.length === 0) {
        setRedirecting(true);
        router.replace('/app/setup');
      }
    });
    return () => {
      live = false;
    };
  }, [router]);

  if (!redirecting) return null;
  return (
    <p role="status" className="rounded-lg border border-amber-500/50 bg-amber-950/20 px-4 py-2 font-mono text-xs text-amber-200">
      no installations detected — opening guided setup…
    </p>
  );
}
