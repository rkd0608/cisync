'use client';

import { useCallback, useEffect, useState, type ReactElement } from 'react';
import { InstallationsTable, type ApiErrorLike } from './installations-table';
import { getInstallationsStatus } from '@/lib/cisync-api';
import type { InstallationsStatusResponse } from '@/lib/installation-schemas';

// Client shell for /installations: owns the resync refetch (read-only) and the
// post-mount clock that makes delivery ages relative. WHY no server-provided
// initial data anymore (B2 SSR fix): SSR cannot resolve the relative gateway
// path, so the server-era "initial fetch" degraded every first paint into a
// bogus unreachable-error. The shell now starts in the syncing posture when
// launched data-less and mounts its own browser-side gateway call — same
// auth semantics on every host, zero deployment env.
export function InstallationsClient({
  initialData = null,
  initialError = null,
}: {
  initialData?: InstallationsStatusResponse | null;
  initialError?: ApiErrorLike | null;
}): ReactElement {
  const [data, setData] = useState<InstallationsStatusResponse | null>(initialData);
  const [error, setError] = useState<ApiErrorLike | null>(initialError);
  // Starting state mirrors reality: with no server data in hand we are
  // genuinely fetching — the table must not flash "no installations".
  const [syncing, setSyncing] = useState(initialData === null && initialError === null);
  const [nowMs, setNowMs] = useState<number | null>(null);

  useEffect(() => {
    // Ages tick on mount and every few seconds so rows visibly age.
    setNowMs(Date.now());
    const timer = setInterval(() => setNowMs(Date.now()), 5000);
    return () => clearInterval(timer);
  }, []);

  const resync = useCallback((): void => {
    setSyncing(true);
    void getInstallationsStatus().then((result) => {
      setData(result.ok ? result.data : null);
      setError(result.ok ? null : { code: result.code, message: result.message });
      setSyncing(false);
    });
  }, []);

  useEffect(() => {
    if (initialData === null && initialError === null) resync();
  }, [resync, initialData, initialError]);

  return (
    <InstallationsTable
      data={data}
      error={error}
      syncing={syncing}
      onResync={resync}
      nowMs={nowMs}
    />
  );
}
