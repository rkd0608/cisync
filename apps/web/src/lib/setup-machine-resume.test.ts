// Resume/persistence half of the setup machine suite — split from
// setup-machine.test.ts to honor the charter's 250-line file cap while both
// halves stay exhaustive.
import { describe, expect, it } from 'vitest';
import {
  initialSetupState,
  parsePersistedSetup,
  reduceSetup,
  resolveEntryState,
  serializeSetup,
  type PersistedSetup,
  type SetupState,
} from './setup-machine';
import { connectEvents, feed } from './setup-machine-fixtures';

describe('setup flow — persistence round-trip', () => {
  it('serialize → parse restores an equivalent snapshot at posture step', () => {
    const atPosture = feed(initialSetupState, [...connectEvents]);
    const restored = parsePersistedSetup(JSON.stringify(serializeSetup(atPosture)));
    expect(restored).toMatchObject({
      version: 1,
      step: 'posture',
      connect: 'connected',
      watch: 'candidate_seen',
      ackNoEnforcement: false,
    });
  });

  it('parsePersistedSetup rejects corrupted payloads instead of trusting them', () => {
    expect(parsePersistedSetup('not json')).toBeNull();
    expect(parsePersistedSetup('{}')).toBeNull();
    expect(parsePersistedSetup(JSON.stringify({ version: 99 }))).toBeNull();
  });

  it('hydrate replaces state wholesale (post-mount resume path)', () => {
    const restored: SetupState = {
      ...initialSetupState,
      step: 'watch',
      connect: 'connected',
      watch: 'listening',
    };
    expect(reduceSetup(initialSetupState, { type: 'hydrate', state: restored })).toBe(restored);
  });
});

describe('resolveEntryState — resume / skip-if-done / restart', () => {
  const savedAtWatch = JSON.stringify({
    version: 1,
    step: 'watch',
    connect: 'connected',
    watch: 'listening',
    ackNoEnforcement: false,
  } satisfies PersistedSetup);

  it('resume: a saved snapshot rehydrates to its saved step', () => {
    const resumed = resolveEntryState({ savedRaw: savedAtWatch });
    expect(resumed).toMatchObject({
      step: 'watch',
      connect: 'connected',
      watch: 'listening',
    });
  });

  it('skip-if-done: live installations fast-forward past install into watching', () => {
    const skipped = resolveEntryState({ savedRaw: null, hasInstallations: true });
    expect(skipped).toMatchObject({
      step: 'watch',
      connect: 'connected',
      watch: 'listening',
    });
  });

  it('fresh tenant with no storage and no installations starts cold at connect', () => {
    expect(resolveEntryState({ savedRaw: null })).toEqual(initialSetupState);
    // Corrupted storage behaves exactly like absent storage.
    expect(resolveEntryState({ savedRaw: '{oops' })).toEqual(initialSetupState);
  });

  it('explicitRestart discards the saved snapshot but still honors live installations', () => {
    const restarted = resolveEntryState({ savedRaw: savedAtWatch, explicitRestart: true });
    expect(restarted).toEqual(initialSetupState);
    const restartedWithRepos = resolveEntryState({
      savedRaw: savedAtWatch,
      explicitRestart: true,
      hasInstallations: true,
    });
    expect(restartedWithRepos.step).toBe('watch');
  });

  it('normalize repairs a stale posture snapshot missing upstream gates', () => {
    const stale = JSON.stringify({
      version: 1,
      step: 'posture',
      connect: 'idle',
      watch: 'idle',
      ackNoEnforcement: false,
    } satisfies PersistedSetup);
    const healed = resolveEntryState({ savedRaw: stale });
    // Posture implies everything before it was done — restore those facts
    // rather than rendering gate-locked dead steps.
    expect(healed).toMatchObject({ step: 'posture', connect: 'connected', watch: 'decision_seen' });
  });
});
