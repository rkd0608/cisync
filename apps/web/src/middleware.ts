// Auth gate for the console surface. WHY verify (not just cookie-presence):
// a stale/tampered cookie must bounce exactly like an absent one; WebCrypto
// verification keeps this runnable on the edge runtime.
import { NextResponse, type NextRequest } from 'next/server';
import { authSecret } from '@/lib/auth-config';
import { verifySession } from '@/lib/auth-session';
import { SESSION_COOKIE } from '@/lib/session-cookie';

export async function middleware(request: NextRequest): Promise<NextResponse> {
  const token = request.cookies.get(SESSION_COOKIE)?.value;
  const secret = authSecret();
  const claims =
    token !== undefined && secret !== null ? await verifySession(token, secret) : null;

  if (claims === null) {
    const loginUrl = new URL('/login', request.url);
    // Round-trip target so post-login lands where the user meant to go.
    loginUrl.searchParams.set('next', request.nextUrl.pathname);
    return NextResponse.redirect(loginUrl);
  }
  return NextResponse.next();
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
    // Guided first-run flow (mission Part 1) lives behind auth like the rest
    // of the console — anonymous visitors bounce to login with next=/app/setup.
    '/app/:path*',
  ],
};
