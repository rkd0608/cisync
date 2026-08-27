import { describe, expect, it } from 'vitest';
import {
  AuthCodeStore,
  CODE_TTL_MS,
  RATE_LIMIT_MAX_REQUESTS,
  RATE_LIMIT_WINDOW_MS,
  generateLoginCode,
} from './auth-code-store';

const EMAIL = 'dev@yourdomain.com';
const T0 = 1_756_200_000_000;

describe('generateLoginCode', () => {
  it('always yields six digits including leading zeros', () => {
    expect(generateLoginCode(() => 0)).toBe('000000');
    expect(generateLoginCode(() => 0.9999999)).toBe('999999');
    expect(generateLoginCode()).toMatch(/^\d{6}$/);
  });
});

describe('issue + consume lifecycle', () => {
  it('accepts a fresh code then consumes it exactly once', async () => {
    const store = new AuthCodeStore();
    await expect(store.issue(EMAIL, '123456', T0)).resolves.toEqual({ ok: true });
    // Wrong code does not burn the valid one.
    await expect(store.consume(EMAIL, '000000', T0 + 1000)).resolves.toBe(false);
    await expect(store.pendingCount()).toBe(1);
    await expect(store.consume(EMAIL, '123456', T0 + 2000)).resolves.toBe(true);
    await expect(store.consume(EMAIL, '123456', T0 + 3000)).resolves.toBe(false);
  });

  it('rejects codes after the 10 minute TTL and evicts them', async () => {
    const store = new AuthCodeStore();
    await store.issue(EMAIL, '654321', T0);
    await expect(store.consume(EMAIL, '999999', T0 + CODE_TTL_MS - 1)).resolves.toBe(false); // wrong code pre-expiry
    await store.issue(EMAIL, '111111', T0);
    await expect(store.consume(EMAIL, '111111', T0 + CODE_TTL_MS - 1)).resolves.toBe(true);
    await store.issue(EMAIL, '222222', T0);
    await expect(store.consume(EMAIL, '222222', T0 + CODE_TTL_MS)).resolves.toBe(false);
    expect(store.pendingCount()).toBe(0);
  });

  it('lets a re-issue supersede the previous outstanding code', async () => {
    const store = new AuthCodeStore();
    await store.issue(EMAIL, '111111', T0);
    await store.issue(EMAIL, '222222', T0 + 5000);
    await expect(store.consume(EMAIL, '111111', T0 + 6000)).resolves.toBe(false);
    await expect(store.consume(EMAIL, '222222', T0 + 7000)).resolves.toBe(true);
  });

  it('is isolated per email', async () => {
    const store = new AuthCodeStore();
    await store.issue('a@yourdomain.com', '123123', T0);
    await store.issue('b@yourdomain.com', '456456', T0);
    await expect(store.consume('a@yourdomain.com', '456456', T0 + 1)).resolves.toBe(false);
    await expect(store.consume('b@yourdomain.com', '456456', T0 + 1)).resolves.toBe(true);
  });
});

describe('per-email rate limit (3/min)', () => {
  it(`allows ${RATE_LIMIT_MAX_REQUESTS} issues then rate-limits with a retry hint`, async () => {
    const store = new AuthCodeStore();
    for (let i = 0; i < RATE_LIMIT_MAX_REQUESTS; i += 1) {
      await expect(store.issue(EMAIL, `10000${i}`, T0 + i)).resolves.toEqual({ ok: true });
    }
    const blocked = await store.issue(EMAIL, '300000', T0 + 4000);
    expect(blocked).toEqual({ ok: false, reason: 'rate_limited', retryAfterS: 56 });
  });

  it('slides the window: after 60s of quiet the email may issue again', async () => {
    const store = new AuthCodeStore();
    for (let i = 0; i < RATE_LIMIT_MAX_REQUESTS; i += 1) {
      await store.issue(EMAIL, `10000${i}`, T0);
    }
    await expect(store.issue(EMAIL, '300000', T0 + RATE_LIMIT_WINDOW_MS)).resolves.toEqual({ ok: true });
  });

  it('applies the limit independently per email', async () => {
    const store = new AuthCodeStore();
    for (let i = 0; i < RATE_LIMIT_MAX_REQUESTS; i += 1) {
      await store.issue(EMAIL, `10000${i}`, T0);
    }
    await expect(
      store.issue('other@yourdomain.com', '400000', T0),
    ).resolves.toEqual({ ok: true });
  });
});
