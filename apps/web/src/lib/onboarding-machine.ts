// Pure state machine for the connect wizard (plan §2.1). Time never enters the
// reducer: the polling cap IS the timeout, so pending→receiving and
// pending→timeout paths are deterministic and unit-testable without fake
// timers. Backend endpoints land in parallel — unreachable backend is a first-
// class honest state ("awaiting backend"), never a crash.
export const MAX_STATUS_POLLS = 20; // 20 × 3s ≈ 60s webhook handshake window

export type ConnectStep = 'install' | 'verify' | 'posture';

export type VerifyStatus =
  | 'idle' // step 1, install not yet confirmed
  | 'polling' // step 2, waiting for any repo to flip to receiving
  | 'linked' // at least one repo webhook_state=receiving
  | 'handshake_timeout' // polls exhausted with no receiving repo
  | 'awaiting_backend'; // endpoint 404/unreachable/schema-mismatch

export interface ConnectWizardState {
  step: ConnectStep;
  verify: VerifyStatus;
  statusPolls: number;
  ackNoEnforcement: boolean;
  finished: boolean;
}

export const initialWizardState: ConnectWizardState = {
  step: 'install',
  verify: 'idle',
  statusPolls: 0,
  ackNoEnforcement: false,
  finished: false,
};

export type WizardEvent =
  | { type: 'start_verify' }
  | { type: 'status_result'; anyReceiving: boolean; backendReachable: boolean }
  | { type: 'retry_verify' }
  | { type: 'open_posture' }
  | { type: 'ack_no_enforcement'; checked: boolean }
  | { type: 'finish' };

export function reduceWizard(
  state: ConnectWizardState,
  event: WizardEvent,
): ConnectWizardState {
  switch (event.type) {
    case 'start_verify':
      return { ...state, step: 'verify', verify: 'polling', statusPolls: 0 };
    case 'status_result': {
      if (state.verify !== 'polling') return state;
      if (!event.backendReachable) return { ...state, verify: 'awaiting_backend' };
      if (event.anyReceiving) return { ...state, verify: 'linked' };
      const statusPolls = state.statusPolls + 1;
      return statusPolls >= MAX_STATUS_POLLS
        ? { ...state, verify: 'handshake_timeout', statusPolls }
        : { ...state, statusPolls };
    }
    case 'retry_verify':
      // Both degraded states recover by restarting the poll window.
      if (state.verify !== 'handshake_timeout' && state.verify !== 'awaiting_backend') {
        return state;
      }
      return { ...state, verify: 'polling', statusPolls: 0 };
    case 'open_posture':
      if (state.verify !== 'linked') return state;
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
