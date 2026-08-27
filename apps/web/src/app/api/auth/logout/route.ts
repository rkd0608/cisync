// POST /api/auth/logout — clears the session cookie unconditionally. Idempotent
// by construction: logging out twice is the same as once. Sessions are
// stateless control-plane JWTs (SPEC §3 2026-08-26); there is nothing to
// revoke server-side — the browser simply drops the jar.

import { NextResponse } from 'next/server';
import { buildSessionClearCookie } from '@/lib/session-cookie';

export async function POST(): Promise<NextResponse> {
  const response = NextResponse.json({ ok: true });
  response.headers.set(
    'Set-Cookie',
    buildSessionClearCookie({ secure: process.env.NODE_ENV === 'production' }),
  );
  return response;
}
