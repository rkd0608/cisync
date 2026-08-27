// POST /api/auth/request-code — passwordless login step 1.
// Boundary order (charter §2): body schema → server config → allowlist →
// rate limit → issue → deliver. Every rejection is explicit; delivery failures
// surface as 502 so the UI never shows "check your inbox" for an unsent mail.

import { NextResponse, type NextRequest } from 'next/server';
import { z } from 'zod';
import { authSecret, emailAllowlist } from '@/lib/auth-config';
import { authCodeStore, generateLoginCode } from '@/lib/auth-code-store';
import { sendLoginCode } from '@/lib/auth-emails';

const requestBodySchema = z.object({
  // Lowercased at the boundary: allowlist + store keys stay canonical.
  email: z.string().trim().toLowerCase().email().max(320),
});

function errorBody(code: string, message: string, retryAfterS?: number): NextResponse {
  return NextResponse.json(
    { error: { code, message, ...(retryAfterS !== undefined ? { retry_after_s: retryAfterS } : {}) } },
    { status: code === 'rate_limited' ? 429 : 400, headers: retryAfterHeaders(retryAfterS) },
  );
}

function retryAfterHeaders(retryAfterS?: number): HeadersInit | undefined {
  if (retryAfterS === undefined) return undefined;
  return { 'Retry-After': String(retryAfterS) };
}

export async function POST(request: NextRequest): Promise<NextResponse> {
  const parsedBody = requestBodySchema.safeParse(await request.json().catch(() => null));
  if (!parsedBody.success) {
    return errorBody('validation_failed', 'request body must be { email: string }');
  }
  const secret = authSecret();
  if (secret === null) {
    return NextResponse.json(
      { error: { code: 'server_misconfigured', message: 'AUTH_SECRET is not configured' } },
      { status: 500 },
    );
  }

  const email = parsedBody.data.email;
  const allowlist = emailAllowlist();
  // Allowlist miscompilation or non-match both deny here — fail closed.
  if (allowlist === null || !allowlist.test(email)) {
    return NextResponse.json(
      { error: { code: 'not_allowed', message: 'email is not on the access allowlist' } },
      { status: 403 },
    );
  }

  const code = generateLoginCode();
  const issued = await authCodeStore.issue(email, code);
  if (!issued.ok) {
    return errorBody(
      'rate_limited',
      `too many codes requested; retry in ${issued.retryAfterS}s`,
      issued.retryAfterS,
    );
  }

  const delivery = await sendLoginCode(email, code);
  if (!delivery.ok) {
    return NextResponse.json(
      { error: { code: 'delivery_failed', message: 'could not send the sign-in email' } },
      { status: 502 },
    );
  }

  // Deliberately identical response for dev-log and resend channels.
  return NextResponse.json({ ok: true });
}
