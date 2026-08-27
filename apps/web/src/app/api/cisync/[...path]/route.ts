// Server-side API gateway for the browser. WHY a route handler instead of
// config rewrites: rewrites cannot inject credentials, and the admin token
// must NEVER ship in the client bundle (charter §2 / THREAT_MODEL B3). The
// browser calls same-origin /api/cisync/v1/*; this handler forwards upstream.
//
// /v1/auth/* special case (SPEC §3 2026-08-26): those paths are PUBLIC on
// control-plane and return session JWTs. This handler (a) never injects the
// admin bearer there, (b) captures {token} from successful login bodies and
// bakes it into an httpOnly cisync_session cookie server-side, (c) never
// passes upstream Set-Cookie headers through. For GET /v1/auth/me it
// translates the cookie into an Authorization bearer — token validation
// always happens at control-plane, the browser never holds a usable token.
import { NextRequest, NextResponse } from 'next/server';
import { extractCookie, sessionCookieFromUpstream, SESSION_COOKIE_NAME } from '@/lib/gateway-auth';

export const dynamic = 'force-dynamic';

const CONTROL_PLANE =
  process.env.CISYNC_API_URL ??
  process.env.NEXT_PUBLIC_CISYNC_API_URL ??
  'http://control-plane:8081';
// Installations live on github-connector (ghconn schema owner), not ctrl.
const CONNECTOR = process.env.CISYNC_CONNECTOR_URL ?? 'http://github-connector:8083';
const ADMIN_TOKEN = process.env.CISYNC_ADMIN_TOKEN ?? '';

function isAuthPath(parts: string[]): boolean {
  return parts[0] === 'v1' && parts[1] === 'auth';
}

function upstreamFor(parts: string[]): string {
  // WHY `v1/auth` listed explicitly: clarity over cleverness in routing.
  return parts[0] === 'v1' && parts[1] === 'installations' ? CONNECTOR : CONTROL_PLANE;
}

const HOP_BY_HOP = new Set([
  'connection',
  'keep-alive',
  'transfer-encoding',
  'upgrade',
  'content-length',
  'host',
]);

// Never copy upstream cookies downstream: control-plane is not the cookie
// authority here; only OUR cisync_session contract may set one.
const STRIP_FROM_RESPONSE = new Set([...HOP_BY_HOP, 'set-cookie']);

function forwardPath(req: NextRequest): string {
  // /api/cisync/v1/x/y -> upstream /v1/x/y
  const parts = req.nextUrl.pathname.split('/').slice(3); // ['', 'api', 'cisync', ...]
  const search = req.nextUrl.search ?? '';
  return `/${parts.join('/')}${search}`;
}

async function handler(req: NextRequest): Promise<NextResponse> {
  const parts = req.nextUrl.pathname.split('/').slice(3);
  const authPath = isAuthPath(parts);

  const headers = new Headers();
  req.headers.forEach((value, key) => {
    if (!HOP_BY_HOP.has(key.toLowerCase())) headers.set(key, value);
  });
  // Public surface: neither client Authorization spoofing nor admin-bearer
  // injection may take part. For /me the session cookie becomes the bearer.
  if (authPath) {
    headers.delete('Authorization');
    headers.delete('Cookie');
    if (parts[2] === 'me') {
      const token = extractCookie(req.headers.get('cookie') ?? undefined, SESSION_COOKIE_NAME);
      if (token !== undefined) headers.set('Authorization', `Bearer ${token}`);
    }
  } else if (!headers.has('Authorization') && ADMIN_TOKEN.length > 0) {
    headers.set('Authorization', `Bearer ${ADMIN_TOKEN}`);
  }

  const body =
    req.method === 'GET' || req.method === 'HEAD' || req.method === 'DELETE'
      ? undefined
      : await req.arrayBuffer();

  let upstreamRes: Response;
  try {
    upstreamRes = await fetch(`${upstreamFor(parts)}${forwardPath(req)}`, {
      method: req.method,
      headers,
      body,
      signal: AbortSignal.timeout(30_000),
    });
  } catch {
    return NextResponse.json(
      { error: { code: 'unavailable', message: 'control-plane unreachable' } },
      { status: 503 },
    );
  }

  const resHeaders = new Headers();
  upstreamRes.headers.forEach((value, key) => {
    if (!STRIP_FROM_RESPONSE.has(key.toLowerCase())) resHeaders.set(key, value);
  });

  // Auth responses need body introspection (token capture), so we buffer them;
  // everything else streams through unchanged, byte-for-byte.
  let payload: ArrayBuffer | null = null;
  if (authPath) {
    payload = await upstreamRes.arrayBuffer();
    const minted = sessionCookieFromUpstream(safeJson(payload), {
      secure: process.env.NODE_ENV === 'production',
    });
    // WHY ok-gated: a 401 error envelope must never mint anything even if an
    // attacker shapes the body; only authenticated login bodies carry tokens.
    if (minted !== null && upstreamRes.ok) resHeaders.set('Set-Cookie', minted);
  }

  return new NextResponse(payload ?? (await upstreamRes.arrayBuffer()), {
    status: upstreamRes.status,
    headers: resHeaders,
  });
}

function safeJson(raw: ArrayBuffer): unknown {
  try {
    return JSON.parse(new TextDecoder().decode(raw));
  } catch {
    return null;
  }
}

export { handler as GET, handler as POST, handler as PUT, handler as PATCH, handler as DELETE };
