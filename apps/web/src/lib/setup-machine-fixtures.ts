import { initialSetupState, reduceSetup, type SetupEvent } from './setup-machine';

// Shared fixtures for the two setup-machine test files (REPO_STANDARDS §1:
// one concern per file — fixture wiring lives alone).
export const connectEvents = [
  { type: 'begin_connect_polling' },
  { type: 'connect_result', anyReceiving: true, backendReachable: true },
  { type: 'begin_watch' },
  { type: 'watch_result', latestCandidateId: 'cand_01J', latestVerb: null, backendReachable: true },
  { type: 'open_posture' },
] as const satisfies SetupEvent[];

export function feed(state: typeof initialSetupState, events: SetupEvent[]): typeof state {
  return events.reduce(reduceSetup, state);
}
