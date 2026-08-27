// POST /api/auth/verify — passwordless login step 2. On success the response
// carries the Set-Cookie session header; the client then hard-navigates to
// /dashboard so middleware sees the fresh cookie.

import { NextResponse, type NextRequest } from 'next/server';
import { z } from 'zod';
import { authSecret } from '@/lib/auth-config';
import { authCodeStore } from '@/lib/auth-code-store';
import { SESSION_TTL_SECONDS, buildSessionSetCookie } from '@/lib/session-cookie';
import { signSession } from '@/lib/auth-session';

const requestBodySchema = z.object({
  email: z.string().trim().toLowerCase().email().max(320),
  code: z.string().regex(/^\d{6}$/, 'code must be exactly six digits'),
});

const THIRTY_DAYS_SECONDS = SESSION_TTL_SECONDS;

export async function POST(request: NextRequest): Promise<NextResponse> {
  const parsedBody = requestBodySchema.safeParse(await request.json().catch(() => null));
  if (!parsedBody.success) {
    return NextResponse.json(
      { error: { code: 'validation_failed', message: 'body must be { email, code } with a 6-digit code' } },
      { status: 400 },
    );
  }

  const secret = authSecret();
  if (secret === null) {
    return NextResponse.json(
      { error: { code: 'server_misconfigured', message: 'AUTH_SECRET is not configured' } },
      { status: 500 },
    );
  }

  const { email, code } = parsedBody.data;
  const valid = await authCodeStore.consume(email, code);
  if (!valid) {
    // Uniform rejection: wrong code, expired code and unknown email are
    // indistinguishable to callers (no enumeration oracle).
    return NextResponse.json(
      { error: { code: 'invalid_code', message: 'code is invalid or expired' } },
      { status: 400 },
    );
  }

  const nowSeconds = Math.floor(Date.now() / 1000);
  const token = await signSession({ email, exp: nowSeconds + THIRTY_DAYS_SECONDS }, secret);
  const response = NextResponse.json({ ok: true, email });
  response.headers.set(
    'Set-Cookie',
    buildSessionSetCookie(token, { secure: process.env.NODE_ENV === 'production' }),
  );
  return response;
}
