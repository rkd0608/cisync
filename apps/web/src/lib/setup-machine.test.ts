// First-run flow state machine unit tests (mission Part 1 requirement: ≥6
// cases including resume + skip — the resume/persistence half lives in
// setup-machine-resume.test.ts). Covers happy path, poll timeout, backend
// absence, monotonic watch signals, and both finish gates.
import { describe, expect, it } from 'vitest';
import {
  MAX_CONNECT_POLLS,
  initialSetupState,
  reduceSetup,
} from './setup-machine';
import { connectEvents, feed } from './setup-machine-fixtures';

describe('setup flow state machine — core transitions', () => {
  it('walks the full happy path connect → watch(candidate) → posture → finished', () => {
    const state = feed(initialSetupState, [
      ...connectEvents,
      { type: 'ack_no_enforcement', checked: true },
      { type: 'finish' },
    ]);
    expect(state).toMatchObject({
      step: 'posture',
      connect: 'connected',
      watch: 'candidate_seen',
      ackNoEnforcement: true,
      finished: true,
    });
  });

  it('decision_seen is reached only through a verb-bearing event and unlocks posture', () => {
    const state = feed(initialSetupState, [
      { type: 'begin_connect_polling' },
      { type: 'connect_result', anyReceiving: true, backendReachable: true },
      { type: 'begin_watch' },
      {
        type: 'watch_result',
        latestCandidateId: 'cand_01J',
        latestVerb: 'eligible_for_merge_train',
        backendReachable: true,
      },
      { type: 'open_posture' },
    ]);
    expect(state.watch).toBe('decision_seen');
    expect(state.step).toBe('posture');
  });

  it('gates posture behind BOTH connect and a watch signal (never dead wizard steps)', () => {
    const withoutSignal = feed(initialSetupState, [
      { type: 'begin_connect_polling' },
      { type: 'connect_result', anyReceiving: true, backendReachable: true },
      { type: 'begin_watch' },
      { type: 'watch_result', latestCandidateId: null, latestVerb: null, backendReachable: true },
    ]);
    expect(reduceSetup(withoutSignal, { type: 'open_posture' })).toBe(withoutSignal);

    const unconnected = feed(initialSetupState, [{ type: 'begin_watch' }]);
    expect(unconnected.step).toBe('connect');
    expect(reduceSetup(unconnected, { type: 'open_posture' })).toBe(unconnected);
  });
});

describe('setup flow — connect polling honesty', () => {
  it('times out to handshake_timeout after exactly MAX_CONNECT_POLLS quiet polls; late success needs retry', () => {
    let state = feed(initialSetupState, [{ type: 'begin_connect_polling' }]);
    for (let poll = 0; poll < MAX_CONNECT_POLLS; poll += 1) {
      state = reduceSetup(state, {
        type: 'connect_result',
        anyReceiving: false,
        backendReachable: true,
      });
    }
    expect(state.connect).toBe('handshake_timeout');
    const recovered = reduceSetup(state, { type: 'retry_connect' });
    expect(recovered).toMatchObject({ connect: 'polling', connectPolls: 0 });
  });

  it('degrades to awaiting_backend when the status endpoint is unreachable and recovers on retry', () => {
    const degraded = feed(initialSetupState, [
      { type: 'begin_connect_polling' },
      { type: 'connect_result', anyReceiving: false, backendReachable: false },
    ]);
    expect(degraded.connect).toBe('awaiting_backend');
    const linked = feed(degraded, [
      { type: 'retry_connect' },
      { type: 'connect_result', anyReceiving: true, backendReachable: true },
    ]);
    expect(linked.connect).toBe('connected');
  });
});

describe('setup flow — watch truthfulness', () => {
  it('signals are monotonic: decision_seen never regresses to candidate_seen', () => {
    const seen = feed(initialSetupState, [
      ...connectEvents.slice(0, 3),
      { type: 'watch_result', latestCandidateId: 'cand_01J', latestVerb: 'deferred', backendReachable: true },
    ]);
    expect(seen.watch).toBe('decision_seen');
    const after = reduceSetup(seen, {
      type: 'watch_result',
      latestCandidateId: 'cand_02K',
      latestVerb: null,
      backendReachable: true,
    });
    expect(after.watch).toBe('decision_seen');
  });

  it('backend loss during listening surfaces awaiting_backend; recovery resumes listening without losing signal', () => {
    let state = feed(initialSetupState, [...connectEvents.slice(0, 3)]);
    state = reduceSetup(state, {
      type: 'watch_result',
      latestCandidateId: null,
      latestVerb: null,
      backendReachable: false,
    });
    expect(state.watch).toBe('awaiting_backend');
    state = reduceSetup(state, {
      type: 'watch_result',
      latestCandidateId: null,
      latestVerb: null,
      backendReachable: false,
    });
    // Still awaiting — a second failure must not silently restart polling.
    expect(state.watch).toBe('awaiting_backend');
    state = reduceSetup(state, {
      type: 'watch_result',
      latestCandidateId: 'cand_03L',
      latestVerb: null,
      backendReachable: true,
    });
    expect(state.watch).toBe('candidate_seen');
  });

  it('ignores watch results when the user never left the connect step', () => {
    const untouched = reduceSetup(initialSetupState, {
      type: 'watch_result',
      latestCandidateId: 'cand_01J',
      latestVerb: null,
      backendReachable: true,
    });
    expect(untouched).toBe(initialSetupState);
  });
});

describe('setup flow — persistence gates', () => {
  it('connect_probe_result skip-if-done only fires on an idle untouched connect step', () => {
    const freshTenant = reduceSetup(initialSetupState, { type: 'connect_probe_result', installationsFound: false });
    expect(freshTenant).toBe(initialSetupState);
    const skipped = reduceSetup(initialSetupState, { type: 'connect_probe_result', installationsFound: true });
    expect(skipped).toMatchObject({ step: 'watch', connect: 'connected', watch: 'listening' });
    // Never yanks a user who is mid-handshake or already past connect.
    const midHandshake = feed(initialSetupState, [{ type: 'begin_connect_polling' }]);
    expect(
      reduceSetup(midHandshake, { type: 'connect_probe_result', installationsFound: true }),
    ).toBe(midHandshake);
  });

  it('refuses to finish without the nothing-enforced acknowledgment (frozen ruling #7)', () => {
    const atPosture = feed(initialSetupState, [...connectEvents]);
    expect(reduceSetup(atPosture, { type: 'finish' }).finished).toBe(false);
    const unchecked = reduceSetup(atPosture, { type: 'ack_no_enforcement', checked: false });
    expect(reduceSetup(unchecked, { type: 'finish' }).finished).toBe(false);
    const checked = reduceSetup(atPosture, { type: 'ack_no_enforcement', checked: true });
    expect(reduceSetup(checked, { type: 'finish' }).finished).toBe(true);
  });

  it('resets the connect poll counter when polling restarts mid-window', () => {
    const midPoll = feed(initialSetupState, [
      { type: 'begin_connect_polling' },
      { type: 'connect_result', anyReceiving: false, backendReachable: true },
      { type: 'connect_result', anyReceiving: false, backendReachable: true },
    ]);
    expect(midPoll.connectPolls).toBe(2);
    expect(
      reduceSetup(midPoll, { type: 'begin_connect_polling' }).connectPolls,
    ).toBe(0);
  });
});

