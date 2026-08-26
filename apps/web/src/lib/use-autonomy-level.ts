'use client';

import { useEffect, useState } from 'react';
import { classifyAutonomy, type AutonomyLevelStatus } from './policy-schema';
import { getActivePolicies } from './cisync-api';

export type AutonomyStatus = 'checking' | AutonomyLevelStatus;

// One-shot autonomy probe for the shadow banner (T5). Any failure — endpoint
// absent, network down, schema drift — resolves to 'unknown' so callers hide
// the banner gracefully instead of blocking or guessing. Never throws.
export function useAutonomyLevel(): AutonomyStatus {
  const [status, setStatus] = useState<AutonomyStatus>('checking');
  useEffect(() => {
    let live = true;
    void getActivePolicies().then((result) => {
      if (!live) return;
      setStatus(result.ok ? classifyAutonomy(result.data.policies) : 'unknown');
    });
    return () => {
      live = false;
    };
  }, []);
  return status;
}
