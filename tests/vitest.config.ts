import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    include: ['invariants/**/*.spec.ts', 'e2e/**/*.spec.ts'],
    testTimeout: 30_000,
    hookTimeout: 30_000,
    // Live/e2e gating happens inside suites via env so a bare `vitest run`
    // stays green (contract mode) with explicit skip reasons, never silent.
  },
});
