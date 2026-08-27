// Cookie contract shared by route handlers, middleware and layouts. Kept free
// of next/server imports so edge middleware and node runtimes both import it.

/** Canonical cookie name. The VALUE inside is a control-plane session JWT
 * (SPEC §3 2026-08-26) — opaque to every web-tier consumer. */
export const SESSION_COOKIE_NAME = 'cisync_session';

export const SESSION_COOKIE = SESSION_COOKIE_NAME; // kept for existing imports

/** 30 days, mirroring the signed claim TTL. */
export const SESSION_TTL_SECONDS = 30 * 24 * 60 * 60;

export interface SessionCookieOptions {
  secure: boolean;
}

export function buildSessionSetCookie(token: string, options: SessionCookieOptions): string {
  // WHY SameSite=Lax over Strict: GitHub App callbacks arrive via top-level
  // navigation and Lax still sends the cookie, keeping post-install flows sane.
  const attributes = [
    `${SESSION_COOKIE_NAME}=${token}`,
    'Path=/',
    'HttpOnly',
    'SameSite=Lax',
    `Max-Age=${SESSION_TTL_SECONDS}`,
  ];
  if (options.secure) attributes.push('Secure');
  return attributes.join('; ');
}

export function buildSessionClearCookie(options: SessionCookieOptions): string {
  const attributes = [
    `${SESSION_COOKIE_NAME}=`,
    'Path=/',
    'HttpOnly',
    'SameSite=Lax',
    'Max-Age=0',
  ];
  if (options.secure) attributes.push('Secure');
  return attributes.join('; ');
}
