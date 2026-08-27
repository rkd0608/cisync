// Gateway-side auth plumbing for /api/cisync/v1/auth/* pass-through
// (SPEC §3 2026-08-26). WHY a separate module: the route handler must stay a
// dumb pipe while EVERY security decision here is a pure function the vitest
// suite can pin without spinning up Next.js — extract-token, cookie mint,
// cookie clear and cookie parse are all byte-contracts with control-plane.
//
// Trust model: the browser never sees the JWT. The gateway captures the
// upstream login token and bakes it into an httpOnly cisync_session cookie
// (SameSite=Lax, Secure in prod); logout and stale sessions are just cookie
// clears — token validation ALWAYS happens against control-plane (/v1/auth/me).

import { buildSessionClearCookie, buildSessionSetCookie, SESSION_COOKIE_NAME } from './session-cookie';

/** Re-exported so consumers have ONE import surface for the auth contract. */
export { SESSION_COOKIE_NAME };

// Fail-closed extraction: anything but a non-empty whitespace-free string is
// rejected so an upstream shape change can never bake garbage into a cookie.
export function extractSessionToken(payload: unknown): string | null {
  if (typeof payload !== 'object' || payload === null) return null;
  const candidate = (payload as Record<string, unknown>)['token'];
  if (typeof candidate !== 'string' || candidate.length === 0 || /\s/.test(candidate)) return null;
  return candidate;
}

export interface CookieOptions {
  secure: boolean;
}

/**
 * Builds the Set-Cookie header for a successful auth upstream payload, or
 * null when the payload carries no usable token (caller passes nothing on).
 */
export function sessionCookieFromUpstream(payload: unknown, options: CookieOptions): string | null {
  const token = extractSessionToken(payload);
  return token !== null ? buildSessionSetCookie(token, options) : null;
}

/** Clear-Cookie header for logout (idempotent by construction upstream). */
export function sessionClearCookie(options: CookieOptions): string {
  return buildSessionClearCookie(options);
}

/** Reads one named cookie out of a raw Cookie header (no decode games). */
export function extractCookie(header: string | undefined, name: string): string | undefined {
  if (header === undefined || header === '') return undefined;
  for (const pair of header.split(';')) {
    const idx = pair.indexOf('=');
    if (idx === -1) continue;
    if (pair.slice(0, idx).trim() === name) {
      const value = pair.slice(idx + 1).trim();
      return value.length > 0 ? value : undefined;
    }
  }
  return undefined;
}
