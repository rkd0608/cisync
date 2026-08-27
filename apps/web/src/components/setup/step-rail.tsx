import type { ReactElement } from 'react';
import type { SetupStep } from '@/lib/setup-machine';

const STEPS: Array<{ index: 1 | 2 | 3; key: SetupStep; title: string }> = [
  { index: 1, key: 'connect', title: 'connect github' },
  { index: 2, key: 'watch', title: 'watch first verification' },
  { index: 3, key: 'posture', title: 'review posture' },
];

export function StepRail({ current }: { current: SetupStep }): ReactElement {
  const currentIndex = STEPS.findIndex((step) => step.key === current);
  return (
    <div
      data-testid="setup-rail"
      className="flex flex-wrap gap-x-5 gap-y-1 font-mono text-[11px] uppercase tracking-widest"
    >
      {STEPS.map((step) => {
        const done = STEPS.findIndex((s) => s.key === step.key) < currentIndex;
        const active = step.key === current;
        return (
          <span
            key={step.key}
            data-step={step.key}
            data-current={active || undefined}
            data-done={done || undefined}
            className={
              active ? 'text-[var(--color-accent-soft)]' : done ? 'text-emerald-400' : 'text-zinc-600'
            }
          >
            {done ? '✓' : '○'} {'①②③'[step.index - 1]} {step.title}
          </span>
        );
      })}
    </div>
  );
}
