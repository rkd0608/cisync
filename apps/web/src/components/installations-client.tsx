'use client';

import { useCallback, useEffect, useState, type ReactElement } from 'react';
import { InstallationsTable, type ApiErrorLike } from './installations-table';
import { getInstallationsStatus } from '@/lib/cisync-api';
import type { InstallationsStatusResponse } from '@/lib/installation-schemas';

// Client shell for /installations: owns the resync refetch (read-only) and the
// post-mount clock that makes delivery ages relative. Server-provided initial
// data renders first; a failed server fetch starts in the honest error state.
export function InstallationsClient({
  initialData,
  initialError,
}: {
  initialData: InstallationsStatusResponse | null;
  initialError: ApiErrorLike | null;
}): ReactElement {
  const [data, setData] = useState<InstallationsStatusResponse | null>(initialData);
  const [error, setError] = useState<ApiErrorLike | null>(initialError);
  const [syncing, setSyncing] = useState(false);
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
