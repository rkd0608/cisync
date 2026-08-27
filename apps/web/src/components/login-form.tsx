'use client';

import { useState, type ReactElement } from 'react';
import { requestCode, verifyCode, type AuthStepError } from '@/lib/login-flow';

export type LoginStep = 'email' | 'code';

const ERROR_COPY: Record<AuthStepError['kind'], string> = {
  network: 'Network unreachable — check your connection and retry.',
  not_allowed: 'This email is not on the access allowlist.',
  rate_limited: 'Too many attempts. Wait a moment, then retry.',
  invalid_code: 'That code is invalid or expired. Request a new one.',
  server: 'Sign-in service error. Try again shortly.',
};

function ErrorNote({ error }: { error: AuthStepError }): ReactElement | null {
  if (error.kind === 'invalid_code' || error.kind === 'server') {
    return (
      <p data-testid="login-error" className="rounded border border-red-500/40 px-3 py-2 font-mono text-[11px] leading-relaxed text-red-300">
        {ERROR_COPY[error.kind]} <span className="text-red-400/60">({error.message})</span>
      </p>
    );
  }
  return (
    <p data-testid="login-error" className="rounded border border-amber-500/40 px-3 py-2 font-mono text-[11px] text-amber-300">
      {ERROR_COPY[error.kind]}
      {error.kind === 'rate_limited' && error.retryAfterS !== undefined ? ` (${error.retryAfterS}s)` : ''}
    </p>
  );
}

const INPUT_CLASS =
  'w-full rounded-md border border-white/15 bg-black/40 px-3 py-2.5 font-mono text-sm text-zinc-100 outline-none transition-colors placeholder:text-zinc-600 focus:border-cyan-400/60 focus:ring-1 focus:ring-cyan-400/40';
const BUTTON_CLASS =
  'w-full rounded-md bg-cyan-500/90 px-4 py-2.5 font-mono text-sm font-semibold text-black transition-colors hover:bg-cyan-400 disabled:cursor-not-allowed disabled:opacity-50';

// Presentational steps are exported separately so vitest can static-render
// each state without a DOM (no jsdom in the approved ledger).
export function LoginEmailStep({
  email,
  onEmailChange,
  onSubmit,
  pending,
}: {
  email: string;
  onEmailChange: (value: string) => void;
  onSubmit: () => void;
  pending: boolean;
}): ReactElement {
  return (
    <form data-testid="login-step-email" className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); onSubmit(); }}>
      <div>
        <h1 className="font-mono text-base tracking-wide text-zinc-100">sign in to CISync</h1>
        <p className="mt-1 text-xs leading-relaxed text-zinc-500">
          Enter your work email and we&apos;ll send a six-digit sign-in code.
        </p>
      </div>
      <label className="flex flex-col gap-1.5 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
        work email
        <input
          type="email"
          required
          autoComplete="email"
          autoFocus
          value={email}
          onChange={(event) => onEmailChange(event.target.value)}
          placeholder="you@yourdomain.com"
          className={INPUT_CLASS}
        />
      </label>
      <button type="submit" disabled={pending || email.trim().length === 0} className={BUTTON_CLASS}>
        {pending ? 'sending…' : 'send code'}
      </button>
    </form>
  );
}

export function LoginCodeStep({
  email,
  code,
  onCodeChange,
  onSubmit,
  onBack,
  pending,
}: {
  email: string;
  code: string;
  onCodeChange: (value: string) => void;
  onSubmit: () => void;
  onBack: () => void;
  pending: boolean;
}): ReactElement {
  return (
    <form data-testid="login-step-code" className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); onSubmit(); }}>
      <div>
        <h1 className="font-mono text-base tracking-wide text-zinc-100">check your inbox</h1>
        <p className="mt-1 text-xs leading-relaxed text-zinc-500">
          Six-digit code sent to <span className="font-mono text-zinc-300">{email}</span>.{' '}
          <button type="button" onClick={onBack} className="text-cyan-400 hover:text-cyan-300">
            use a different email
          </button>
        </p>
      </div>
      <label className="flex flex-col gap-1.5 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
        sign-in code
        <input
          inputMode="numeric"
          pattern="\d{6}"
          maxLength={6}
          required
          autoFocus
          value={code}
          onChange={(event) => onCodeChange(event.target.value.replace(/\D/g, '').slice(0, 6))}
          placeholder="000000"
          className={`${INPUT_CLASS} text-center text-lg tracking-[0.5em]`}
        />
      </label>
      <button type="submit" disabled={pending || code.length !== 6} className={BUTTON_CLASS}>
        {pending ? 'verifying…' : 'verify & continue'}
      </button>
    </form>
  );
}

export function LoginForm(): ReactElement {
  const [step, setStep] = useState<LoginStep>('email');
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<AuthStepError | null>(null);

  async function submitEmail(): Promise<void> {
    setPending(true);
    setError(null);
    const result = await requestCode(email.trim(), fetch);
    setPending(false);
    if (result.ok) {
      setStep('code');
    } else {
      setError(result.error);
    }
  }

  async function submitCode(): Promise<void> {
    setPending(true);
    setError(null);
    const result = await verifyCode(email.trim(), code, fetch);
    if (!result.ok) {
      setPending(false);
      setError(result.error);
      return;
    }
    // Full navigation (not router.push) so middleware re-evaluates the fresh
    // httpOnly cookie; honors ?next= from the auth redirect.
    const params = new URLSearchParams(window.location.search);
    const target = params.get('next');
    window.location.assign(target !== null && target.startsWith('/') ? target : '/dashboard');
  }

  return (
    <div className="flex flex-col gap-4" data-testid="login-form" data-step={step}>
      {step === 'email' ? (
        <LoginEmailStep email={email} onEmailChange={setEmail} onSubmit={submitEmail} pending={pending} />
      ) : (
        <LoginCodeStep
          email={email}
          code={code}
          onCodeChange={setCode}
          onSubmit={submitCode}
          onBack={() => { setStep('email'); setError(null); }}
          pending={pending}
        />
      )}
      {error !== null ? <ErrorNote error={error} /> : null}
    </div>
  );
}
