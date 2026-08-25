import { describe, expect, it } from 'vitest';
import {
  MAX_STATUS_POLLS,
  initialWizardState,
  reduceWizard,
  type WizardEvent,
} from './onboarding-machine';

const feed = (state: Parameters<typeof reduceWizard>[0], events: WizardEvent[]) =>
  events.reduce(reduceWizard, state);

describe('wizard step state machine', () => {
  it('walks the happy path install → linked → posture → finished', () => {
    const state = feed(initialWizardState, [
      { type: 'start_verify' },
      { type: 'status_result', anyReceiving: true, backendReachable: true },
      { type: 'open_posture' },
      { type: 'ack_no_enforcement', checked: true },
      { type: 'finish' },
    ]);
    expect(state).toMatchObject({
      step: 'posture',
      verify: 'linked',
      ackNoEnforcement: true,
      finished: true,
    });
  });

  it('stays pending while repos exist but none is receiving yet', () => {
    let state = reduceWizard(initialWizardState, { type: 'start_verify' });
    for (let poll = 1; poll < MAX_STATUS_POLLS; poll += 1) {
      state = reduceWizard(state, {
        type: 'status_result',
        anyReceiving: false,
        backendReachable: true,
      });
    }
    expect(state.verify).toBe('polling');
    expect(state.statusPolls).toBe(MAX_STATUS_POLLS - 1);
  });

  it('times out to handshake_timeout after exactly MAX_STATUS_POLLS quiet polls', () => {
    let state = reduceWizard(initialWizardState, { type: 'start_verify' });
    for (let poll = 0; poll < MAX_STATUS_POLLS; poll += 1) {
      state = reduceWizard(state, {
        type: 'status_result',
        anyReceiving: false,
        backendReachable: true,
      });
    }
    expect(state.verify).toBe('handshake_timeout');
    // A late success still completes even after timeout was reached…
    const recovered = reduceWizard(state, {
      type: 'status_result',
      anyReceiving: true,
      backendReachable: true,
    });
    expect(recovered.verify).toBe('handshake_timeout'); // …only via explicit retry
    expect(
      reduceWizard(recovered, { type: 'retry_verify' }).verify,
    ).toBe('polling');
  });

  it('degrades to awaiting_backend when the endpoint is absent, and retries', () => {
    const degraded = feed(initialWizardState, [
      { type: 'start_verify' },
      { type: 'status_result', anyReceiving: false, backendReachable: false },
    ]);
    expect(degraded.verify).toBe('awaiting_backend');

    const retried = reduceWizard(degraded, { type: 'retry_verify' });
    expect(retried).toMatchObject({ verify: 'polling', statusPolls: 0 });

    // Backend came up on retry and flips a repo → linked.
    const linked = reduceWizard(retried, {
      type: 'status_result',
      anyReceiving: true,
      backendReachable: true,
    });
    expect(linked.verify).toBe('linked');
  });

  it('ignores status results outside the polling window (stale async replies)', () => {
    const stale = reduceWizard(initialWizardState, {
      type: 'status_result',
      anyReceiving: true,
      backendReachable: true,
    });
    expect(stale).toBe(initialWizardState);
  });

  it('gates posture review behind the linked state (never dead buttons)', () => {
    const premature = reduceWizard(initialWizardState, { type: 'open_posture' });
    expect(premature.step).toBe('install');
    expect(premature).toBe(initialWizardState);
  });

  it('refuses to finish without the nothing-enforced acknowledgment', () => {
    const atPosture = feed(initialWizardState, [
      { type: 'start_verify' },
      { type: 'status_result', anyReceiving: true, backendReachable: true },
      { type: 'open_posture' },
    ]);
    expect(
      reduceWizard(atPosture, { type: 'finish' }).finished,
    ).toBe(false);
    const unchecked = reduceWizard(atPosture, {
      type: 'ack_no_enforcement',
      checked: false,
    });
    expect(reduceWizard(unchecked, { type: 'finish' }).finished).toBe(false);
    const checked = reduceWizard(atPosture, {
      type: 'ack_no_enforcement',
      checked: true,
    });
    expect(reduceWizard(checked, { type: 'finish' }).finished).toBe(true);
  });

  it('resets the poll counter when start_verify is re-dispatched', () => {
    const midPoll = feed(initialWizardState, [
      { type: 'start_verify' },
      { type: 'status_result', anyReceiving: false, backendReachable: true },
      { type: 'status_result', anyReceiving: false, backendReachable: true },
    ]);
    expect(midPoll.statusPolls).toBe(2);
    expect(
      reduceWizard(midPoll, { type: 'start_verify' }).statusPolls,
    ).toBe(0);
  });
});
