import { describe, expect, it } from 'vitest';
import {
  isSetupComplete,
  loadSavedSetup,
  markSetupComplete,
  saveSetupState,
  windowStorageOrNull,
} from './setup-storage';
import { initialSetupState } from './setup-machine';

// Minimal in-memory Storage stub — storage behavior is the contract here,
// not browser internals.
function memoryStorage(): Storage {
  const map = new Map<string, string>();
  return {
    get length() {
      return map.size;
    },
    clear: () => map.clear(),
    getItem: (key) => (map.has(key) ? (map.get(key) as string) : null),
    key: (index) => [...map.keys()][index] ?? null,
    removeItem: (key) => void map.delete(key),
    setItem: (key, value) => void map.set(key, String(value)),
  };
}

function throwingStorage(): Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> {
  return {
    getItem: () => {
      throw new Error('blocked');
    },
    setItem: () => {
      throw new Error('blocked');
    },
    removeItem: () => {
      throw new Error('blocked');
    },
  };
}

describe('setup-storage boundary', () => {
  it('round-trips a serializable setup snapshot', () => {
    const store = memoryStorage();
    saveSetupState(store, { ...initialSetupState, step: 'watch', connect: 'connected' });
    expect(loadSavedSetup(store)).toContain('"step":"watch"');
  });

  it('markSetupComplete writes the tombstone and removes the in-flight snapshot', () => {
    const store = memoryStorage();
    saveSetupState(store, initialSetupState);
    markSetupComplete(store);
    expect(isSetupComplete(store)).toBe(true);
    expect(loadSavedSetup(store)).toBeNull();
  });

  it('storage failures never throw — callers degrade to session-only progress', () => {
    const hostile = throwingStorage();
    expect(() => saveSetupState(hostile, initialSetupState)).not.toThrow();
    expect(loadSavedSetup(hostile)).toBeNull();
    expect(() => markSetupComplete(hostile)).not.toThrow();
    expect(isSetupComplete(hostile)).toBe(false);
  });

  it('windowStorageOrNull returns null off-window (SSR safety)', () => {
    // Vitest node environment has no window — exactly the SSR case.
    expect(windowStorageOrNull()).toBeNull();
  });
});
