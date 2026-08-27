// Login-code delivery. WHY Resend via raw fetch instead of an SDK: the only
// operation we need is POST /emails, and the dependency ledger approves
// `resend` as optional — a fetch call keeps the no-key dev path identical and
// avoids shipping SDK weight for one endpoint.

import { resendApiKey } from './auth-config';

const RESEND_ENDPOINT = 'https://api.resend.com/emails';
const MAIL_FROM = process.env.CISYNC_AUTH_MAIL_FROM ?? 'CISync <onboarding@resend.dev>';

export type DeliveryResult =
  | { ok: true; channel: 'resend' | 'dev-log' }
  | { ok: false; channel: 'resend'; message: string };

function codeEmailBody(code: string): string {
  return [
    'Your CISync sign-in code:',
    '',
    `    ${code}`,
    '',
    'It expires in 10 minutes and can be used once.',
    'If you did not request it, ignore this email.',
  ].join('\n');
}

export async function sendLoginCode(email: string, code: string): Promise<DeliveryResult> {
  const apiKey = resendApiKey();
  if (apiKey === null) {
    // WHY logged server-side: without RESEND_API_KEY there is no mail provider;
    // surfacing the code in server logs keeps local/dev flows usable end-to-end
    // while never exposing it to the client bundle or API responses.
    console.log(
      `[auth] DEV MODE — RESEND_API_KEY not set. Login code for ${email}: ${code} ` +
        '(valid 10 minutes, single use). Set RESEND_API_KEY to deliver by email.',
    );
    return { ok: true, channel: 'dev-log' };
  }

  try {
    const response = await fetch(RESEND_ENDPOINT, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${apiKey}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        from: MAIL_FROM,
        to: [email],
        subject: `${code} is your CISync sign-in code`,
        text: codeEmailBody(code),
      }),
      signal: AbortSignal.timeout(10_000),
    });
    if (!response.ok) {
      const detail = await response.text().catch(() => '');
      return {
        ok: false,
        channel: 'resend',
        message: `resend returned HTTP ${response.status}${detail.length > 0 ? `: ${detail.slice(0, 200)}` : ''}`,
      };
    }
    return { ok: true, channel: 'resend' };
  } catch (cause) {
    return { ok: false, channel: 'resend', message: `resend unreachable: ${String(cause)}` };
  }
}
