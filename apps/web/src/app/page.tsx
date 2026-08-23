import { IntentBoard } from '@/components/intent-board';

export default function DashboardPage(): React.ReactElement {
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="font-mono text-lg tracking-wide text-zinc-100">live board</h1>
        <p className="mt-1 max-w-3xl text-sm text-zinc-500">
          Derived from the control-plane ledger tail. The v1 contract exposes no
          intent-list endpoint, so this board projects events the same way every
          other consumer does.
        </p>
      </div>
      <IntentBoard />
    </div>
  );
}
