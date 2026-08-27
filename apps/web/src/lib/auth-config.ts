// Runtime env accessors for the auth surface. WHY centralized: every env var
// is read through exactly one typed function so misconfiguration is detectable
// and fail-closed (charter §2: env config is an input boundary).
//
// Email+password sessions replaced OTP email delivery (SPEC §3 2026-08-26):
// there is NO AUTH_SECRET / RESEND / allowlist anymore — the session JWT is
// minted and verified by control-plane (CISYNC_SESSION_KEY_FILE); the web
// tier only carries the token inside an httpOnly cookie.

export type SignupMode = 'open' | 'invite' | 'closed';

const SIGNUP_MODES: SignupMode[] = ['open', 'invite', 'closed'];

// WHY default 'invite': signup must not silently open to the world if an
// operator forgets CISYNC_SIGNUP_MODE; open-ness is always explicit.
const DEFAULT_SIGNUP_MODE: SignupMode = 'invite';

export function signupMode(rawEnv?: string): SignupMode {
  const raw = (rawEnv ?? process.env.CISYNC_SIGNUP_MODE ?? '').trim().toLowerCase();
  return SIGNUP_MODES.includes(raw as SignupMode) ? (raw as SignupMode) : DEFAULT_SIGNUP_MODE;
}

export function githubAppInstallUrl(): string | null {
  const url = process.env.NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL;
  return url !== undefined && url.length > 0 ? url : null;
}
