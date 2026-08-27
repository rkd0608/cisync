'use client';

import { useState, type ReactElement } from 'react';
import { logIn, signUp, type AuthStepError } from '@/lib/auth-flow';
import type { SignupMode } from '@/lib/auth-config';

const ERROR_COPY: Record<AuthStepError['kind'], string> = {
  network: 'Network unreachable — check your connection and retry.',
  invalid_credentials: 'Invalid email or password.',
  weak_password: 'Password must be at least 10 characters.',
  invalid_email: 'That does not look like a valid email address.',
  exists: 'An account with this email already exists — sign in instead.',
  rate_limited: 'Too many attempts. Wait a moment, then retry.',
  server: 'Sign-in service error. Try again shortly.',
};

// WHY two tones: credential-policy errors are user expectations (amber);
// anything else is service trouble and gets the louder red treatment.
function isErrorTone(kind: AuthStepError['kind']): boolean {
  return (
    kind === 'weak_password' ||
    kind === 'invalid_email' ||
    kind === 'exists' ||
    kind === 'invalid_credentials'
  );
}

const INPUT_CLASS =
  'w-full rounded-md border border-white/15 bg-black/40 px-3 py-2.5 font-mono text-sm text-zinc-100 outline-none transition-colors placeholder:text-zinc-400 focus:border-cyan-400/60 focus:ring-1 focus:ring-cyan-400/40';
const BUTTON_CLASS =
  'w-full rounded-md bg-cyan-500/90 px-4 py-2.5 font-mono text-sm font-semibold text-black transition-colors hover:bg-cyan-400 disabled:cursor-not-allowed disabled:opacity-50';

export interface CredentialsFormProps {
  mode: SignupMode;
  isSignup: boolean;
  onToggleSignup: () => void;
  email: string;
  onEmailChange: (value: string) => void;
  password: string;
  onPasswordChange: (value: string) => void;
  onSubmit: () => void;
  pending: boolean;
  error: AuthStepError | null;
}

// Presentational form body; exported so vitest can static-render every state
// without a DOM (the approved ledger has no jsdom).
export function CredentialsFormView(props: CredentialsFormProps): ReactElement {
  const { mode, isSignup, onToggleSignup, email, onEmailChange, password, onPasswordChange, onSubmit, pending, error } = props;
  const canSubmit = email.includes('@') && password.length >= 8;
  return (
    <div className="flex flex-col gap-4" data-testid="login-form" data-mode={isSignup ? 'signup' : 'signin'}>
      <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); onSubmit(); }}>
        <div>
          <h1 className="font-mono text-base tracking-wide text-zinc-100">
            {isSignup ? 'create your CISync account' : 'sign in to CISync'}
          </h1>
          <p className="mt-1 text-xs leading-relaxed text-zinc-500">
            {isSignup ? 'One email, one password. No magic links.' : 'Email and password. Sessions last 30 days.'}
          </p>
        </div>
        <label className="flex flex-col gap-1.5 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          email
          <input
            type="email"
            required
            autoComplete="email"
            value={email}
            onChange={(event) => onEmailChange(event.target.value)}
            placeholder="you@yourdomain.com"
            className={INPUT_CLASS}
          />
        </label>
        <label className="flex flex-col gap-1.5 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          password
          <input
            type="password"
            required
            autoComplete={isSignup ? 'new-password' : 'current-password'}
            value={password}
            onChange={(event) => onPasswordChange(event.target.value)}
            placeholder="at least 10 characters"
            minLength={10}
            className={INPUT_CLASS}
          />
        </label>
        <button type="submit" data-testid="auth-submit" disabled={pending || !canSubmit} className={BUTTON_CLASS}>
          {pending ? 'working…' : isSignup ? 'create account' : 'sign in'}
        </button>
      </form>
      {mode === 'open' ? (
        <p className="text-center text-xs text-zinc-500" data-testid="signup-toggle">
          {isSignup ? 'already registered?' : 'need an account?'}{' '}
          <button
            type="button"
            onClick={onToggleSignup}
            className="text-cyan-400 hover:text-cyan-300"
          >
            {isSignup ? 'sign in' : 'create one'}
          </button>
        </p>
      ) : null}
      {error !== null ? (
        <p
          data-testid="login-error"
          className={`rounded px-3 py-2 font-mono text-[11px] ${
            isErrorTone(error.kind)
              ? 'border border-amber-500/40 text-amber-300'
              : 'border border-red-500/40 leading-relaxed text-red-300'
          }`}
        >
          {ERROR_COPY[error.kind]}
          {error.kind === 'rate_limited' && error.retryAfterS !== undefined ? ` (${error.retryAfterS}s)` : ''}
        </p>
      ) : null}
      {mode !== 'open' ? (
        <p data-testid="closed-copy" className="mt-1 text-center font-mono text-[10px] uppercase tracking-widest text-zinc-500">
          sign-ups are invite-only · contact your administrator
        </p>
      ) : null}
    </div>
  );
}

// Stateful island: owns the field/toggle/pending state and the network calls.
// WHY it stays thin: every network transition lives in lib/auth-flow.ts so it
// stays stubbable, and this component only feeds it inputs.
export function LoginForm({ mode }: { mode: SignupMode }): ReactElement {
  const [isSignup, setIsSignup] = useState(false);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<AuthStepError | null>(null);

  // WHY full navigation over router.push: middleware must re-evaluate the
  // freshly-set httpOnly cookie on a real request, and ?next= from the auth
  // redirect has to survive the trip.
  function navigateAfterAuth(): void {
    const params = new URLSearchParams(window.location.search);
    const target = params.get('next');
    window.location.assign(target !== null && target.startsWith('/') ? target : '/dashboard');
  }

  async function submit(): Promise<void> {
    setPending(true);
    setError(null);
    const result = isSignup
      ? await signUp(email.trim(), password, fetch)
      : await logIn(email.trim(), password, fetch);
    if (!result.ok) {
      setPending(false);
      setError(result.error);
      return;
    }
    if (isSignup) {
      // Account created → immediately authenticate to mint the session cookie.
      const login = await logIn(email.trim(), password, fetch);
      if (!login.ok) {
        setIsSignup(false); // account now exists; regular sign-in covers retries
        setPending(false);
        setError(login.error);
        return;
      }
    }
    navigateAfterAuth();
  }

  return (
    <CredentialsFormView
      mode={mode}
      isSignup={isSignup}
      onToggleSignup={() => { setIsSignup(!isSignup); setError(null); }}
      email={email}
      onEmailChange={setEmail}
      password={password}
      onPasswordChange={setPassword}
      onSubmit={submit}
      pending={pending}
      error={error}
    />
  );
}
