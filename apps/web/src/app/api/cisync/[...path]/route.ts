// Server-side API gateway for the browser. WHY a route handler instead of
// config rewrites: rewrites cannot inject credentials, and the admin token
// must NEVER ship in the client bundle (charter §2 / THREAT_MODEL B3). The
// browser calls same-origin /api/cisync/v1/*; this handler forwards upstream
// with the bearer injected from runtime env. Malformed upstream responses are
// passed through unchanged — schema validation stays client-side fail-closed.
import { NextRequest, NextResponse } from 'next/server';

export const dynamic = 'force-dynamic';

const CONTROL_PLANE =
  process.env.CISYNC_API_URL ??
  process.env.NEXT_PUBLIC_CISYNC_API_URL ??
  'http://control-plane:8081';
// Installations live on github-connector (ghconn schema owner), not ctrl.
const CONNECTOR = process.env.CISYNC_CONNECTOR_URL ?? 'http://github-connector:8083';
const ADMIN_TOKEN = process.env.CISYNC_ADMIN_TOKEN ?? '';

function upstreamFor(parts: string[]): string {
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

function forwardPath(req: NextRequest): string {
  // /api/cisync/v1/x/y -> upstream /v1/x/y
  const parts = req.nextUrl.pathname.split('/').slice(3); // ['', 'api', 'cisync', ...]
  const search = req.nextUrl.search ?? '';
  return `/${parts.join('/')}${search}`;
}

async function handler(req: NextRequest): Promise<NextResponse> {
  const headers = new Headers();
  req.headers.forEach((value, key) => {
    if (!HOP_BY_HOP.has(key.toLowerCase())) headers.set(key, value);
  });
  if (!headers.has('Authorization') && ADMIN_TOKEN.length > 0) {
    headers.set('Authorization', `Bearer ${ADMIN_TOKEN}`);
  }

  const body =
    req.method === 'GET' || req.method === 'HEAD' || req.method === 'DELETE'
      ? undefined
      : await req.arrayBuffer();

  let upstreamRes: Response;
  try {
    const parts = req.nextUrl.pathname.split('/').slice(3);
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
    if (!HOP_BY_HOP.has(key.toLowerCase())) resHeaders.set(key, value);
  });
  const payload = await upstreamRes.arrayBuffer();
  return new NextResponse(payload, { status: upstreamRes.status, headers: resHeaders });
}

export {
  handler as GET,
  handler as POST,
  handler as PUT,
  handler as PATCH,
  handler as DELETE,
};
