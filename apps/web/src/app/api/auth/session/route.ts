// GET /api/auth/session — whoami for the browser. 200 {email} with a valid
// signed cookie, else 401. No partial trust: an unverifiable token IS anonymous.

import { cookies } from 'next/headers';
import { NextResponse } from 'next/server';
import { authSecret } from '@/lib/auth-config';
import { verifySession } from '@/lib/auth-session';
import { SESSION_COOKIE } from '@/lib/session-cookie';

export const dynamic = 'force-dynamic';

export async function GET(): Promise<NextResponse> {
  const jar = await cookies();
  const token = jar.get(SESSION_COOKIE)?.value;
  const secret = authSecret();
  if (token === undefined || secret === null) {
    return NextResponse.json({ error: { code: 'unauthenticated', message: 'no valid session' } }, { status: 401 });
  }
  const claims = await verifySession(token, secret);
  if (claims === null) {
    return NextResponse.json({ error: { code: 'unauthenticated', message: 'no valid session' } }, { status: 401 });
  }
  return NextResponse.json({ email: claims.email });
}
