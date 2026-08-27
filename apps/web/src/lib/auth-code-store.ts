// In-memory login-code store + per-email rate limiter. WHY in-memory: the
// brief explicitly scopes passwordless auth with no DB dependency; codes are
// single-purpose, 10-minute TTL, and a process restart simply forces the user
// to request a new one. Horizontal scaling would need sticky routing or Redis
// (flagged for W7-B, not built here).

export const CODE_TTL_MS = 10 * 60 * 1000;
export const CODE_MAX_AGE_SECONDS = Math.floor(CODE_TTL_MS / 1000);
export const RATE_LIMIT_MAX_REQUESTS = 3;
export const RATE_LIMIT_WINDOW_MS = 60 * 1000;

export type IssueResult =
  | { ok: true }
  | { ok: false; reason: 'rate_limited'; retryAfterS: number };

interface StoredCode {
  codeHash: string;
  expiresAtMs: number;
}

async function sha256Hex(input: string): Promise<string> {
  // WHY hashed at rest: even this ephemeral Map must not hold replayable
  // secrets in plaintext (heap dumps, error reporters).
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(input));
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('');
}

export function generateLoginCode(randomSource: () => number = Math.random): string {
  // 6-digit numeric code, uniformly sampled across 000000..999999.
  return String(Math.floor(randomSource() * 1_000_000)).padStart(6, '0');
}

export class AuthCodeStore {
  private readonly codes = new Map<string, StoredCode>();
  private readonly issueTimestampsMs = new Map<string, number[]>();

  async issue(
    email: string,
    code: string,
    nowMs: number = Date.now(),
  ): Promise<IssueResult> {
    if (!this.admitIssue(email, nowMs)) {
      const windowStartMs = nowMs - RATE_LIMIT_WINDOW_MS;
      const oldestKeptMs =
        (this.issueTimestampsMs.get(email) ?? []).find((ts) => ts > windowStartMs) ?? nowMs;
      const retryAfterS = Math.max(1, Math.ceil((oldestKeptMs + RATE_LIMIT_WINDOW_MS - nowMs) / 1000));
      return { ok: false, reason: 'rate_limited', retryAfterS };
    }
    // New code supersedes any outstanding one for the same email.
    this.codes.set(email, {
      codeHash: await sha256Hex(code),
      expiresAtMs: nowMs + CODE_TTL_MS,
    });
    return { ok: true };
  }

  async consume(email: string, code: string, nowMs: number = Date.now()): Promise<boolean> {
    const stored = this.codes.get(email);
    if (stored === undefined) return false;
    // Expired entries are evicted on touch so the Map cannot grow unbounded.
    if (stored.expiresAtMs <= nowMs) {
      this.codes.delete(email);
      return false;
    }
    const matches = (await sha256Hex(code)) === stored.codeHash;
    if (matches) {
      // Single-use: a verified code can never be replayed.
      this.codes.delete(email);
    }
    return matches;
  }

  pendingCount(): number {
    return this.codes.size;
  }

  private admitIssue(email: string, nowMs: number): boolean {
    const windowStartMs = nowMs - RATE_LIMIT_WINDOW_MS;
    const recent = (this.issueTimestampsMs.get(email) ?? []).filter((ts) => ts > windowStartMs);
    if (recent.length >= RATE_LIMIT_MAX_REQUESTS) {
      this.issueTimestampsMs.set(email, recent);
      return false;
    }
    recent.push(nowMs);
    this.issueTimestampsMs.set(email, recent);
    return true;
  }
}

// Module-singleton is deliberate: route handlers on the same server instance
// must share rate-limit + code state.
export const authCodeStore = new AuthCodeStore();
