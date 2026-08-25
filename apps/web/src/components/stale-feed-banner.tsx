'use client';

import { useEffect, useState, type ReactElement } from 'react';
import { isFeedStale } from '@/lib/event-board';

function useOneSecondTick(): number {
  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNowMs(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  return nowMs;
}

// §7 latency contract made visible: >60s without a seq advance ⇒ amber banner
// citing the last seq we actually hold ("showing snapshot at seq N"). Pure
// presentation — it never gates the board itself.
export function StaleFeedBannerView({
  stale,
  lastSeq,
}: {
  stale: boolean;
  lastSeq: number;
}): ReactElement | null {
  if (!stale) return null;
  return (
    <div
      role="status"
      data-testid="stale-feed"
      className="rounded border border-amber-500/50 bg-amber-950/20 px-4 py-2 font-mono text-xs text-amber-200"
    >
      live feed paused — showing snapshot at seq {lastSeq}
    </div>
  );
}

export function StaleFeedBanner({
  lastSeq,
  lastAdvancedAtMs,
}: {
  lastSeq: number;
  lastAdvancedAtMs: number | null;
}): ReactElement | null {
  const nowMs = useOneSecondTick();
  return <StaleFeedBannerView stale={isFeedStale(lastAdvancedAtMs, nowMs)} lastSeq={lastSeq} />;
}
