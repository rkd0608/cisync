// Auth gate for the console surface. WHY oracle-call instead of local HMAC
// verification: sessions are control-plane-signed Ed25519 JWTs carried in an
// httpOnly cookie (SPEC §3 2026-08-26); validation happens ONE place —
// GET /v1/auth/me via the same-origin gateway — so tampered, stale or revoked
// cookies bounce exactly like absent ones, with identical /login?next= UX.
import { NextResponse, type NextRequest } from 'next/server';
import { checkSessionViaGateway } from '@/lib/middleware-auth';

export const HEADER_AUTH_EMAIL = 'x-cisync-auth-email';

export async function middleware(request: NextRequest): Promise<NextResponse> {
  const result = await checkSessionViaGateway(
    request.nextUrl.origin,
    request.headers.get('cookie') ?? undefined,
  );

  if (!result.authenticated) {
    const loginUrl = new URL('/login', request.url);
    // Round-trip target so post-login lands where the user meant to go.
    loginUrl.searchParams.set('next', request.nextUrl.pathname);
    return NextResponse.redirect(loginUrl);
  }

  // Forward the oracle-verified identity to server components (the console
  // layout renders it in the header). WHY a request header and not a client
  // hint: the value crossed the /me trust boundary INSIDE this middleware,
  // downstream code can rely on it without re-verifying signatures.
  const headers = new Headers(request.headers);
  if (result.email !== null) headers.set(HEADER_AUTH_EMAIL, result.email);
  return NextResponse.next({ request: { headers } });
}

export const config = {
  matcher: [
    '/dashboard/:path*',
    '/intents/:path*',
    '/candidates/:path*',
    '/clusters/:path*',
    '/installations/:path*',
    '/decisions/:path*',
    '/settings/:path*',
    // Guided first-run flow lives behind auth like the rest of the console.
    '/app/:path*',
  ],
};
