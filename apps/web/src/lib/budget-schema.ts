// G7 GET /v1/budgets (PRODUCT_UX_PLAN §6, LATER wave): the strip must appear
// GRACEFULLY when the endpoint ships and stay hidden while it doesn't. Every
// field is optional so a partial rollout renders the meters it has, and the
// whole object fails to null on 404/schema drift rather than blocking the
// dashboard.
import { z } from 'zod';

const usageSchema = z
  .object({
    limit: z.number().nonnegative().optional(),
    consumed: z.number().nonnegative().optional(),
  })
  .partial();

export const budgetsResponseSchema = z.object({
  tenant_hour: z.record(z.string(), usageSchema).optional(),
});

export type BudgetUsage = { limit?: number; consumed?: number };
export type BudgetsResponse = z.infer<typeof budgetsResponseSchema>;

export interface BudgetMeter {
  label: string;
  consumed: number | null;
  limit: number | null;
}

// Named rows we know how to render today; unknown keys are ignored so the
// backend can add categories without breaking the strip.
const METER_LABELS: Array<{ key: string; label: string }> = [
  { key: 'cpu_minutes', label: 'cpu-min/hr' },
  { key: 'concurrent_candidates', label: 'concurrent cands' },
  { key: 'env_templates', label: 'env leases' },
];

export function deriveBudgetMeters(response: BudgetsResponse): BudgetMeter[] {
  const tenantHour = response.tenant_hour ?? {};
  return METER_LABELS.flatMap(({ key, label }) => {
    const parsed = usageSchema.safeParse(tenantHour[key]);
    if (!parsed.success) return [];
    const usage = parsed.data;
    // A row with neither number is not information — drop it instead of
    // rendering an empty meter.
    if (usage.consumed === undefined && usage.limit === undefined) return [];
    return [{ label, consumed: usage.consumed ?? null, limit: usage.limit ?? null }];
  });
}
