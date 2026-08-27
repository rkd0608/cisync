// Stateless signed-cookie session primitives. WHY WebCrypto (crypto.subtle)
// instead of node:crypto: these functions are called from Next.js middleware,
// which runs on the edge runtime where node:* modules cannot load. WebCrypto
// is available in edge, Node ≥20 and vitest alike, with zero dependencies.
//
// Token format: base64url(JSON claims) + "." + base64url(HMAC-SHA256 payload).
// Claims carry only { email, exp } (unix seconds) — no PII beyond the email the
// user typed, no roles yet (v0.2 has a single admin scope).

export interface SessionClaims {
  email: string;
  /** Expiry as UNIX seconds. */
  exp: number;
}

const encoder = new TextEncoder();

// WHY explicit <ArrayBuffer> type args: TS 5.7 generics distinguish
// SharedArrayBuffer-backed views; WebCrypto accepts only ArrayBuffer views
// and our copies are always freshly allocated.
function encodeUtf8(input: string): Uint8Array<ArrayBuffer> {
  return new Uint8Array(encoder.encode(input));
}

function toBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function fromBase64Url(value: string): Uint8Array<ArrayBuffer> | null {
  const padded = value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (value.length % 4)) % 4);
  try {
    const binary = atob(padded);
    const out = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i += 1) out[i] = binary.charCodeAt(i);
    return out;
  } catch {
    return null;
  }
}

async function hmac(secret: string, payload: Uint8Array<ArrayBuffer>): Promise<string> {
  const key = await crypto.subtle.importKey(
    'raw',
    encoder.encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  );
  const signature = await crypto.subtle.sign('HMAC', key, payload);
  return toBase64Url(new Uint8Array(signature));
}

// WHY manual loop: crypto.subtle has no constant-time compare on the edge;
// length-checked XOR accumulation avoids early-exit leaks for this low-stakes
// comparison (token is also bounded to same-origin httpOnly cookie).
function signaturesMatch(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i += 1) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

export async function signSession(
  claims: SessionClaims,
  secret: string,
): Promise<string> {
  const payload = toBase64Url(encodeUtf8(JSON.stringify(claims)));
  const signature = await hmac(secret, encodeUtf8(payload));
  return `${payload}.${signature}`;
}

export async function verifySession(
  token: string,
  secret: string,
  nowMs: number = Date.now(),
): Promise<SessionClaims | null> {
  const parts = token.split('.');
  if (parts.length !== 2) return null;
  const [payload, signature] = parts;

  const expected = await hmac(secret, encodeUtf8(payload));
  if (!signaturesMatch(signature, expected)) return null;

  const raw = fromBase64Url(payload);
  if (raw === null) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(new TextDecoder().decode(raw));
  } catch {
    return null;
  }
  if (typeof parsed !== 'object' || parsed === null) return null;
  const candidate = parsed as Record<string, unknown>;
  const email = candidate.email;
  const exp = candidate.exp;
  if (typeof email !== 'string' || email.length === 0 || email.length > 320) return null;
  if (typeof exp !== 'number' || !Number.isFinite(exp)) return null;
  // Expired tokens verify cryptographically but fail temporally here — callers
  // treat null uniformly and re-authenticate.
  if (exp * 1000 <= nowMs) return null;
  return { email, exp };
}
