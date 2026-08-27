import { describe, expect, it } from 'vitest';
import { signSession, verifySession } from './auth-session';

const SECRET = 'test-secret-at-least-16-chars';
const FIXED_NOW_MS = 1_756_200_000_000; // arbitrary fixed instant

function claimsAt(expSeconds: number): { email: string; exp: number } {
  return { email: 'agent@yourdomain.com', exp: expSeconds };
}

describe('auth-session roundtrip', () => {
  it('signs and verifies a session, returning the original claims', async () => {
    const token = await signSession(claimsAt(Math.floor(FIXED_NOW_MS / 1000) + 60), SECRET);
    const verified = await verifySession(token, SECRET, FIXED_NOW_MS);
    expect(verified).not.toBeNull();
    expect(verified?.email).toBe('agent@yourdomain.com');
    expect(verified?.exp).toBe(Math.floor(FIXED_NOW_MS / 1000) + 60);
  });

  it('produces two-segment base64url tokens', async () => {
    const token = await signSession(claimsAt(Math.floor(FIXED_NOW_MS / 1000) + 60), SECRET);
    expect(token).toMatch(/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/);
  });
});

describe('auth-session rejection paths (all yield null, never throw)', () => {
  it('rejects a token signed with a different secret', async () => {
    const token = await signSession(claimsAt(Math.floor(FIXED_NOW_MS / 1000) + 60), SECRET);
    await expect(verifySession(token, 'another-secret-16-characters!', FIXED_NOW_MS)).resolves.toBeNull();
  });

  it('rejects a tampered payload while keeping the signature', async () => {
    const token = await signSession(claimsAt(Math.floor(FIXED_NOW_MS / 1000) + 60), SECRET);
    const [payload, signature] = token.split('.');
    const forgedPayload = Buffer.from(
      JSON.stringify({ email: 'attacker@yourdomain.com', exp: Math.floor(FIXED_NOW_MS / 1000) + 9999 }),
    ).toString('base64url');
    await expect(verifySession(`${forgedPayload}.${signature}`, SECRET, FIXED_NOW_MS)).resolves.toBeNull();
    // Sanity: untouched payload still verifies.
    await expect(verifySession(`${payload}.${signature}`, SECRET, FIXED_NOW_MS)).resolves.not.toBeNull();
  });

  it('rejects an expired session at the temporal check', async () => {
    const token = await signSession(claimsAt(Math.floor(FIXED_NOW_MS / 1000) - 1), SECRET);
    await expect(verifySession(token, SECRET, FIXED_NOW_MS)).resolves.toBeNull();
  });

  it('accepts a session exactly until exp, one ms later rejects', async () => {
    const expSeconds = Math.floor(FIXED_NOW_MS / 1000) + 10;
    const token = await signSession({ email: 'a@yourdomain.com', exp: expSeconds }, SECRET);
    await expect(verifySession(token, SECRET, expSeconds * 1000 - 1)).resolves.not.toBeNull();
    await expect(verifySession(token, SECRET, expSeconds * 1000)).resolves.toBeNull();
  });

  it.each([
    ['empty string', ''],
    ['no separator', 'garbagepayload'],
    ['too many segments', 'a.b.c'],
    ['non-JSON payload', `${Buffer.from('not json').toString('base64url')}.c2ln`],
    [
      'wrong claim shapes',
      `${Buffer.from(JSON.stringify({ email: 42, exp: 'soon' })).toString('base64url')}.c2ln`,
    ],
  ])('treats %s as unverified', async (_label, token) => {
    await expect(verifySession(token, SECRET, FIXED_NOW_MS)).resolves.toBeNull();
  });
});
