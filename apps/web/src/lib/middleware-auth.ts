// Middleware auth oracle (SPEC §3 2026-08-26). WHY the cookie is validated
// by calling control-plane GET /v1/auth/me through the same-origin gateway
// instead of verifying locally: the session JWT is signed with control-plane's
// Ed25519 key; edge code must never need that key material or a local crypto
// twin of it. One round-trip per matched navigation keeps a single source of
// truth — a tampered, stale or revoked cookie bounces EXACTLY like an absent
// one (same /login?next= UX as before).
//
// Kept free of next/server imports so vitest exercises it with a stubbed
// fetch on plain Node.

export interface OracleResult {
  authenticated: boolean;
  /** Verified email echo from upstream; null unless 200. */
  email: string | null;
}

interface MeBody {
  user?: { email?: unknown };
}

/**
 * Calls GET {origin}/api/cisync/v1/auth/me forwarding ONLY the session
 * cookie (never the browser's Authorization header — that must not be able
 * to impersonate a session) and reports whether upstream said 200.
 *
 * WHY any failure maps to unauthenticated: gateway unreachable or DB down
 * cannot become an auth bypass; users simply get bounced to /login and the
 * redirect flow retries next navigation.
 */
export async function checkSessionViaGateway(
  origin: string,
  rawCookieHeader: string | undefined,
  fetchImpl: typeof fetch = fetch,
): Promise<OracleResult> {
  try {
    const headers: Record<string, string> = {};
    if (rawCookieHeader !== undefined && rawCookieHeader !== '') {
      headers.cookie = rawCookieHeader;
    }
    const response = await fetchImpl(`${origin}/api/cisync/v1/auth/me`, {
      method: 'GET',
      headers,
      // Bounded so a wedged upstream can't stall navigation forever.
      signal: AbortSignal.timeout(8_000),
    });
    if (!response.ok) return { authenticated: false, email: null };
    const body = (await response.json()) as MeBody;
    const email =
      typeof body?.user?.email === 'string' && body.user.email.length > 0 ? body.user.email : null;
    return { authenticated: email !== null, email };
  } catch {
    return { authenticated: false, email: null };
  }
}
