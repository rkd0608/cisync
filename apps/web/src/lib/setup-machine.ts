// Pure state machine for the guided FIRST-RUN flow at /app/setup (mission
// Part 1). Supersedes the v0.2 connect wizard by adding: step-2 "first
// verification watch" over the ledger tail, localStorage resume/skip-if-done,
// and auto-skip of steps already satisfied by live tenant state. Time never
// enters the reducer — poll caps ARE timeouts, so every transition is
// deterministic and unit-testable without fake timers.
import { z } from 'zod';

export const MAX_CONNECT_POLLS = 20; // 20 × 3s ≈ 60s webhook handshake window

export type SetupStep = 'connect' | 'watch' | 'posture';

export type ConnectStatus =
  | 'idle'
  | 'polling'
  | 'connected'
  | 'handshake_timeout'
  | 'awaiting_backend';

// Watch states tell the truth about what the ledger has produced so far:
// nothing yet ≠ broken; backend down is a first-class honest state.
export type WatchStatus =
  | 'idle'
  | 'listening'
  | 'candidate_seen'
  | 'decision_seen'
  | 'awaiting_backend';

export interface SetupState {
  step: SetupStep;
  connect: ConnectStatus;
  connectPolls: number;
  watch: WatchStatus;
  ackNoEnforcement: boolean;
  finished: boolean;
}

export const initialSetupState: SetupState = {
  step: 'connect',
  connect: 'idle',
  connectPolls: 0,
  watch: 'idle',
  ackNoEnforcement: false,
  finished: false,
};

export type SetupEvent =
  | { type: 'hydrate'; state: SetupState }
  | { type: 'begin_connect_polling' }
  // Mount-time probe result used purely for skip-if-done: an untouched flow
  // whose tenant ALREADY reports installations jumps straight to watching.
  | { type: 'connect_probe_result'; installationsFound: boolean }
  | { type: 'connect_result'; anyReceiving: boolean; backendReachable: boolean }
  | { type: 'retry_connect' }
  | { type: 'begin_watch' }
  | {
      type: 'watch_result';
      latestCandidateId: string | null;
      latestVerb: string | null;
      backendReachable: boolean;
    }
  | { type: 'open_posture' }
  | { type: 'ack_no_enforcement'; checked: boolean }
  | { type: 'finish' };

const WATCH_SIGNAL_REACHED: WatchStatus[] = ['candidate_seen', 'decision_seen'];

function watchAdvance(
  current: WatchStatus,
  event: Extract<SetupEvent, { type: 'watch_result' }>,
): WatchStatus {
  if (!event.backendReachable) return current === 'listening' ? 'awaiting_backend' : current;
  if (event.latestVerb !== null) return 'decision_seen';
  if (event.latestCandidateId !== null) {
    // Monotonic: once a decision lands we never regress to candidate-only.
    return WATCH_SIGNAL_REACHED.includes(current) ? current : 'candidate_seen';
  }
  return current === 'awaiting_backend' ? 'listening' : current;
}

export function reduceSetup(state: SetupState, event: SetupEvent): SetupState {
  switch (event.type) {
    case 'hydrate':
      // Post-mount restore of a persisted snapshot. Hydrate-as-event keeps
      // the client's first render identical to SSR markup (no mismatch).
      return event.state;
    case 'begin_connect_polling':
      return { ...state, connect: 'polling', connectPolls: 0 };
    case 'connect_probe_result':
      // Skip-if-done for fresh browsers (no localStorage): only an IDLE,
      // untouched connect step may be fast-forwarded — a user mid-handshake
      // or beyond must never be yanked backwards.
      if (!(state.step === 'connect' && state.connect === 'idle')) return state;
      if (!event.installationsFound) return state;
      return { ...state, step: 'watch', connect: 'connected', watch: 'listening' };
    case 'connect_result': {
      if (state.connect !== 'polling') return state;
      if (!event.backendReachable) return { ...state, connect: 'awaiting_backend' };
      if (event.anyReceiving) return { ...state, connect: 'connected' };
      const connectPolls = state.connectPolls + 1;
      return connectPolls >= MAX_CONNECT_POLLS
        ? { ...state, connect: 'handshake_timeout', connectPolls }
        : { ...state, connectPolls };
    }
    case 'retry_connect':
      if (state.connect !== 'handshake_timeout' && state.connect !== 'awaiting_backend') {
        return state;
      }
      return { ...state, connect: 'polling', connectPolls: 0 };
    case 'begin_watch':
      // Watching proves *events* flow, which is meaningless until at least
      // one repo receives webhooks — the UI surfaces this as disabled, and
      // the machine enforces it even against stray events.
      if (state.connect !== 'connected') return state;
      return { ...state, step: 'watch', watch: state.watch === 'idle' ? 'listening' : state.watch };
    case 'watch_result':
      // Results are accepted in every non-idle watch state so a late reply
      // that flips listening → decision_seen after a candidate_seen pass
      // still lands; idle means the user never started watching.
      if (state.watch === 'idle') return state;
      return { ...state, watch: watchAdvance(state.watch, event) };
    case 'open_posture':
      // Posture requires BOTH earlier gates (frozen ruling #7 stays: the
      // checkbox is the enforcement-ack gate; watching proves events flow).
      if (state.connect !== 'connected' || !WATCH_SIGNAL_REACHED.includes(state.watch)) {
        return state;
      }
      return { ...state, step: 'posture' };
    case 'ack_no_enforcement':
      if (state.step !== 'posture') return state;
      return { ...state, ackNoEnforcement: event.checked };
    case 'finish':
      // The checkbox is the gate: finishing without acknowledging is a no-op.
      if (state.step !== 'posture' || !state.ackNoEnforcement) return state;
      return { ...state, finished: true };
    default:
      return state;
  }
}

// ---------- persistence contract ----------
//
// Only a minimal snapshot persists: enough to resume/skip, never id-level
// detail (installations are re-checked live). Zod validates the boundary so a
// corrupted or stale localStorage payload falls back to a fresh run instead
// of poisoning the flow.

export const persistedSetupSchema = z.object({
  version: z.literal(1),
  step: z.enum(['connect', 'watch', 'posture']),
  connect: z.enum(['idle', 'polling', 'connected', 'handshake_timeout', 'awaiting_backend']),
  watch: z.enum(['idle', 'listening', 'candidate_seen', 'decision_seen', 'awaiting_backend']),
  ackNoEnforcement: z.boolean(),
});

export type PersistedSetup = z.infer<typeof persistedSetupSchema>;

export function serializeSetup(state: SetupState): PersistedSetup {
  const watchSaved =
    state.watch === 'idle' && state.step !== 'connect'
      ? 'listening' // completed connect implies watch was reachable
      : state.watch;
  return persistedSetupSchema.parse({
    version: 1,
    step: state.step,
    connect: state.connect,
    watch: watchSaved,
    ackNoEnforcement: state.ackNoEnforcement,
  });
}

function normalize(persisted: PersistedSetup): SetupState {
  // Structural repair for stale snapshots: posture requires both upstream
  // gates, and a finished/non-started connect must not fake "connected".
  let connect = persisted.connect === 'polling' || persisted.connect === 'idle' ? 'idle' : persisted.connect;
  const watchRestored =
    persisted.step === 'posture' && !WATCH_SIGNAL_REACHED.includes(persisted.watch as WatchStatus)
      ? 'decision_seen'
      : persisted.watch;
  if (persisted.step === 'posture' && connect !== 'connected') connect = 'connected';
  return {
    ...initialSetupState,
    step: persisted.step,
    connect: connect as ConnectStatus,
    watch: watchRestored as WatchStatus,
    ackNoEnforcement: persisted.ackNoEnforcement,
  };
}

export function parsePersistedSetup(raw: string): PersistedSetup | null {
  try {
    const parsed: unknown = JSON.parse(raw);
    const result = persistedSetupSchema.safeParse(parsed);
    return result.success ? result.data : null;
  } catch {
    return null;
  }
}

export interface ResumeOptions {
  /** raw localStorage payload (or null when absent/blocked) */
  savedRaw: string | null;
  /** live tenant already reports ≥1 installation → connect step is done */
  hasInstallations?: boolean;
  /** user re-entered via /onboarding deep link after finishing previously */
  explicitRestart?: boolean;
}

/**
 * Entry-point resolution: skip-if-done beats resume beats fresh. A returning
 * user who already connected AND watched resumes at their furthest saved
 * step; fresh tenants with pre-existing installations skip straight past the
 * install CTA into the watch loop.
 */
export function resolveEntryState(options: ResumeOptions): SetupState {
  if (!options.explicitRestart && options.savedRaw !== null) {
    const persisted = parsePersistedSetup(options.savedRaw);
    if (persisted !== null) return normalize(persisted);
  }
  if (options.hasInstallations) {
    return {
      ...initialSetupState,
      step: 'watch',
      connect: 'connected',
      watch: 'listening',
    };
  }
  return initialSetupState;
}
