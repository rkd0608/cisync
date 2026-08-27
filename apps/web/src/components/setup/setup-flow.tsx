'use client';

import { useRouter } from 'next/navigation';
import { useCallback, useEffect, useReducer, useRef, useState, type ReactElement } from 'react';
import { getEvents, getInstallationsStatus } from '@/lib/cisync-api';
import { anyRepoReceiving, type InstallationStatus } from '@/lib/installation-schemas';
import {
  initialSetupState,
  reduceSetup,
  resolveEntryState,
} from '@/lib/setup-machine';
import {
  isSetupComplete,
  loadSavedSetup,
  markSetupComplete,
  saveSetupState,
  windowStorageOrNull,
} from '@/lib/setup-storage';
import { ConnectStep, type ConnectedRepo } from './connect-step';
import { FinishBar, PostureStep } from './posture-step';
import { StepRail } from './step-rail';
import { WatchStep, type WatchSignal } from './watch-step';

const POLL_INTERVAL_MS = 3000;
const WATCH_EVENT_TYPES = ['candidate.submitted', 'decision.rendered'];

function toConnectedRepos(installations: InstallationStatus[]): ConnectedRepo[] {
  return installations.flatMap((installation) =>
    installation.repos.map((repo) => ({
      account: installation.account,
      name: repo.name,
      webhookState: repo.webhook_state,
    })),
  );
}

// First-run orchestrator (mission Part 1): pure machine + two honest poll
// loops + localStorage resume/skip-if-done. Rendering splits into per-step
// components; this file owns ONLY wiring. Poll timers self-schedule while —
// and only while — their machine state demands it.
export function SetupFlow(): ReactElement {
  const router = useRouter();
  const [state, dispatch] = useReducer(reduceSetup, initialSetupState);
  const [installations, setInstallations] = useState<InstallationStatus[] | null>(null);
  const [signal, setSignal] = useState<WatchSignal>({ candidateId: null, verb: null, confidence: null });
  const [alreadyDone, setAlreadyDone] = useState(false);
  const stoppedRef = useRef(false);

  // Post-mount restore: localStorage + completed tombstone decide entry
  // (resume vs done-screen). Post-mount keeps SSR/client markup identical.
  useEffect(() => {
    stoppedRef.current = false;
    const store = windowStorageOrNull();
    if (store === null) return;
    if (isSetupComplete(store)) {
      setAlreadyDone(true);
      return;
    }
    dispatch({ type: 'hydrate', state: resolveEntryState({ savedRaw: loadSavedSetup(store) }) });
    return () => {
      stoppedRef.current = true;
    };
  }, []);

  // Silent skip-if-done probe: a tenant whose installations already exist
  // never sees the install CTA again, even on a fresh browser with no local
  // snapshot. Endpoint down ⇒ CTA flow proceeds; nothing pretends.
  useEffect(() => {
    let live = true;
    void getInstallationsStatus().then((result) => {
      if (!live || !result.ok) return;
      setInstallations(result.data.installations);
      dispatch({ type: 'connect_probe_result', installationsFound: result.data.installations.length > 0 });
    });
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    saveSetupState(windowStorageOrNull(), state);
  }, [state]);

  // Connect handshake loop: runs while state.connect === 'polling'. The
  // reschedule rides a LOCAL attempt counter — re-dispatching
  // begin_connect_polling would reset the machine's poll budget each tick.
  const [connectAttempt, setConnectAttempt] = useState(0);
  useEffect(() => {
    if (state.connect !== 'polling') return;
    let timer: ReturnType<typeof setTimeout> | null = null;
    void (async () => {
      const result = await getInstallationsStatus();
      if (stoppedRef.current) return;
      if (result.ok) setInstallations(result.data.installations);
      dispatch({
        type: 'connect_result',
        anyReceiving: result.ok && anyRepoReceiving(result.data.installations),
        backendReachable: result.ok,
      });
      timer = setTimeout(() => setConnectAttempt((attempt) => attempt + 1), POLL_INTERVAL_MS);
    })();
    return () => {
      if (timer !== null) clearTimeout(timer);
    };
  }, [state.connect, connectAttempt]);

  // Watch loop: re-polls on every non-terminal watch change so a seen
  // candidate upgrades live to a decision; halted once posture is reached.
  useEffect(() => {
    if (state.step !== 'watch' || state.watch === 'idle') return;
    let live = true;
    const timer = setInterval(async () => {
      const result = await getEvents({ afterSeq: 0, types: WATCH_EVENT_TYPES, limit: 100 });
      if (!live || !result.ok) {
        // Backend flaps degrade honestly instead of wiping observed signal.
        dispatch({ type: 'watch_result', latestCandidateId: null, latestVerb: null, backendReachable: false });
        return;
      }
      let latestCandidateId: string | null = null;
      let latestVerb: string | null = null;
      let confidence: number | null = null;
      for (const event of result.data.events) {
        if (event.type === 'candidate.submitted') {
          const id = (event.payload as { candidate_id?: unknown }).candidate_id;
          if (typeof id === 'string') latestCandidateId = id;
        } else if (event.type === 'decision.rendered') {
          const payload = event.payload as { verb?: unknown; confidence?: unknown };
          if (typeof payload.verb === 'string') {
            latestVerb = payload.verb;
            confidence = typeof payload.confidence === 'number' ? payload.confidence : null;
          }
        }
      }
      setSignal({ candidateId: latestCandidateId, verb: latestVerb, confidence });
      dispatch({ type: 'watch_result', latestCandidateId, latestVerb, backendReachable: true });
    }, POLL_INTERVAL_MS);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [state.step, state.watch]);

  const handleInstalled = useCallback(() => dispatch({ type: 'begin_connect_polling' }), []);
  const handleRetryConnect = useCallback(() => dispatch({ type: 'retry_connect' }), []);
  const handleOpenPosture = useCallback(() => dispatch({ type: 'open_posture' }), []);
  const handleAck = useCallback(
    (checked: boolean) => dispatch({ type: 'ack_no_enforcement', checked }),
    [],
  );

  function finish(): void {
    markSetupComplete(windowStorageOrNull());
    router.push('/dashboard');
  }

  return (
    <div className="flex flex-col gap-6">
      <StepRail current={alreadyDone ? 'posture' : state.step} />
      {alreadyDone ? (
        <div data-testid="setup-already-done" className="rounded-lg border border-dashed border-emerald-500/40 px-5 py-8 text-center">
          <p className="font-mono text-xs uppercase tracking-widest text-emerald-300">✓ setup already complete</p>
          <p className="mx-auto mt-2 max-w-md text-xs leading-relaxed text-zinc-400">
            This workspace finished guided setup before — going straight to the board is safe. Re-run the flow only for a brand-new tenant.
          </p>
          <button type="button" onClick={finish} className="btn-console mt-4 w-fit">
            go to dashboard →
          </button>
        </div>
      ) : (
        <>
          {state.step === 'connect' ? (
            <ConnectStep
              connect={state.connect}
              polls={state.connectPolls}
              repos={toConnectedRepos(installations ?? [])}
              onInstalled={handleInstalled}
              onRetry={handleRetryConnect}
            />
          ) : null}
          {state.step === 'watch' ? (
            <WatchStep watch={state.watch} signal={signal} onContinue={handleOpenPosture} />
          ) : null}
          {state.step === 'posture' ? (
            <PostureStep state={state} onAckChange={handleAck} />
          ) : null}
          {state.step === 'posture' ? (
            <FinishBar
              disabled={!state.ackNoEnforcement}
              finished={state.finished}
              onFinish={() => dispatch({ type: 'finish' })}
            />
          ) : null}
        </>
      )}
    </div>
  );
}
