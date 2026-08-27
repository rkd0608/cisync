// Runtime env accessors for the auth surface. WHY centralized: every env var
// is read through exactly one typed function so misconfiguration is detectable
// and fail-closed (charter §2: env config is an input boundary).
//
// CISYNC_AUTH_EMAIL_PATTERN: allowlist regex (string form) that a requester's
// email must FULLY match (anchored by convention, e.g. ".*@yourdomain\\.com$").
// Default below is intentionally restrictive so a forgotten env var can never
// open registration to the world.

const DEFAULT_EMAIL_PATTERN = '.*@yourdomain\\.com$';

let cachedPattern: RegExp | null | undefined;

// WHY cached: RegExp compilation per request would hide a broken pattern until
// traffic arrives; we compile once and remember failure as `null` (deny-all).
export function emailAllowlist(): RegExp | null {
  if (cachedPattern === undefined) {
    const source = process.env.CISYNC_AUTH_EMAIL_PATTERN ?? DEFAULT_EMAIL_PATTERN;
    try {
      cachedPattern = new RegExp(source);
    } catch {
      // Fail closed: an unparseable operator pattern rejects every email.
      cachedPattern = null;
    }
  }
  return cachedPattern;
}

export function resetEmailAllowlistCacheForTests(): void {
  cachedPattern = undefined;
}

// WHY null instead of a default secret: sessions signed with a guessable key
// are an authentication bypass, so absence must disable auth entirely.
export function authSecret(): string | null {
  const secret = process.env.AUTH_SECRET;
  return secret !== undefined && secret.length >= 16 ? secret : null;
}

export function resendApiKey(): string | null {
  const key = process.env.RESEND_API_KEY;
  return key !== undefined && key.length > 0 ? key : null;
}

export function githubAppInstallUrl(): string | null {
  const url = process.env.NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL;
  return url !== undefined && url.length > 0 ? url : null;
}

export const DEFAULT_EMAIL_PATTERN_SOURCE = DEFAULT_EMAIL_PATTERN;
