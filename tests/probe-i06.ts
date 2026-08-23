import { authedHeaders, apiBase, expectErrorBody } from './invariants/lib/live.js';
import { intentGrantSchema } from './invariants/lib/api-schemas.js';
import { HttpError, newIdempotencyKey, request } from './invariants/lib/http.js';

const outcomes: string[] = [];
const results = await Promise.allSettled(
  Array.from({ length: 24 }, (_, i) =>
    request(
      {
        url: `${apiBase()}/change-intents`,
        method: 'POST',
        headers: { ...authedHeaders(), 'Idempotency-Key': newIdempotencyKey(`probe-burst-${i}`) },
        body: {
          goal: `budget burst ${i}`, repository: `acme/burst-${i % 3}`, base: 'main',
          expected_surfaces: ['services/**'], acceptance_criteria: ['x'], risk: 'low',
        },
      },
      intentGrantSchema,
    ),
  ),
);
for (const r of results) {
  if (r.status === 'fulfilled') { outcomes.push(`granted:${r.value.status}`); continue; }
  if (r.reason instanceof HttpError) {
    try {
      const envelope = await expectErrorBody(r.reason.status, r.reason.rawBody);
      outcomes.push(`${r.reason.status}:${envelope.error.code}`);
    } catch (e) {
      outcomes.push(`${r.reason.status}:UNPARSEABLE ${String(e).slice(0, 120)} raw=${r.reason.rawBody.slice(0, 120)}`);
    }
  } else {
    outcomes.push(`REJECTED ${String(r.reason).slice(0, 200)}`);
  }
}
console.log(outcomes.join('\n'));
