// localStorage boundary for the setup flow (mission Part 1: persist step
// state). WHY a dedicated module: window access is IO, so it stays out of the
// pure reducer; every read/write is guarded because storage can throw
// (Safari private mode, quota, disabled) and an unavailable store must never
// block onboarding — it degrades to session-only progress.
import { serializeSetup, type SetupState } from './setup-machine';

const SETUP_STATE_KEY = 'cisync.setup.v1';
const SETUP_DONE_KEY = 'cisync.setup.done';

// All writers/readers accept `null` (SSR / blocked store) and no-op or return
// absence — callers never need casts, and a dead store can never throw.
export function loadSavedSetup(storage: Pick<Storage, 'getItem'> | null): string | null {
  if (storage === null) return null;
  try {
    return storage.getItem(SETUP_STATE_KEY);
  } catch {
    return null;
  }
}

export function saveSetupState(
  storage: Pick<Storage, 'setItem'> | null,
  state: SetupState,
): void {
  if (storage === null || state.finished) return; // finished is recorded by markSetupComplete
  try {
    storage.setItem(SETUP_STATE_KEY, JSON.stringify(serializeSetup(state)));
  } catch {
    // Storage refused (quota/private mode): flow continues session-only.
  }
}

export function markSetupComplete(
  storage: Pick<Storage, 'removeItem' | 'setItem'> | null,
): void {
  if (storage === null) return;
  try {
    // Remove the in-flight snapshot AND stamp done — skip-if-done reads only
    // the tombstone so a stale partial snapshot can never resurrect.
    storage.removeItem(SETUP_STATE_KEY);
    storage.setItem(SETUP_DONE_KEY, new Date().toISOString());
  } catch {
    return;
  }
}

export function isSetupComplete(storage: Pick<Storage, 'getItem'> | null): boolean {
  if (storage === null) return false;
  try {
    return storage.getItem(SETUP_DONE_KEY) !== null;
  } catch {
    return false;
  }
}

/** SSR/window guard: server callers always see a dead store. */
export function windowStorageOrNull(): Storage | null {
  if (typeof window === 'undefined') return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}
