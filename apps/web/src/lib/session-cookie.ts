// Cookie contract shared by route handlers, middleware and layouts. Kept free
// of next/server imports so edge middleware and node runtimes both import it.

export const SESSION_COOKIE = 'cisync_session';

/** 30 days, mirroring the signed claim TTL. */
export const SESSION_TTL_SECONDS = 30 * 24 * 60 * 60;

export interface SessionCookieOptions {
  secure: boolean;
}

export function buildSessionSetCookie(token: string, options: SessionCookieOptions): string {
  // WHY SameSite=Lax over Strict: GitHub App callbacks arrive via top-level
  // navigation and Lax still sends the cookie, keeping post-install flows sane.
  const attributes = [
    `${SESSION_COOKIE}=${token}`,
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
    `${SESSION_COOKIE}=`,
    'Path=/',
    'HttpOnly',
    'SameSite=Lax',
    'Max-Age=0',
  ];
  if (options.secure) attributes.push('Secure');
  return attributes.join('; ');
}
