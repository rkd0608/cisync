// POST /api/auth/logout — clears the session cookie unconditionally. Idempotent
// by construction: logging out twice is the same as once.

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
