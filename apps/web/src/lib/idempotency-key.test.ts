import { describe, expect, it } from 'vitest';
import { isValidIdempotencyKey, newIdempotencyKey } from './idempotency-key';

describe('newIdempotencyKey', () => {
  it('satisfies the openapi header contract (16..128 chars)', () => {
    const key = newIdempotencyKey();
    expect(isValidIdempotencyKey(key)).toBe(true);
  });

  it('produces unique keys across calls', () => {
    const seen = new Set(Array.from({ length: 50 }, () => newIdempotencyKey()));
    expect(seen.size).toBe(50);
  });

  it('rejects keys outside the contract window', () => {
    expect(isValidIdempotencyKey('short')).toBe(false);
    expect(isValidIdempotencyKey('x'.repeat(129))).toBe(false);
    expect(isValidIdempotencyKey('x'.repeat(16))).toBe(true);
  });
});
