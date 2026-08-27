'use client';

import { useRouter, useSearchParams } from 'next/navigation';
import { useMemo, type ReactElement } from 'react';
import { BoardFilterBar } from './board-filter-bar';
import { BudgetStrip, useBudgetMeters } from './budget-strip';
import { FirstRunRedirect } from './first-run-redirect';
import { IntentBoard } from './intent-board';
import { ShadowBanner } from './shadow-banner';
import { StaleFeedBanner } from './stale-feed-banner';
import {
  ALL_FILTERS,
  boardQueryString,
  candidateMatchesFilters,
  distinctOrigins,
  distinctRepos,
  intentMatchesFilters,
  parseBoardFilters,
} from '@/lib/board-filters';
import { RISK_CLASSES } from '@/lib/api-schemas';
import { useEventBoard } from '@/lib/use-event-board';

// Command-center composition (mission Part 3): shadow banner + first-run
// interception + query-param-persisted filters/search + budget strip (hidden
// until the endpoint exists) over the ledger-derived swimlane board. The
// board itself never blocks on the banners.
export function BoardWorkspace(): ReactElement {
  const handle = useEventBoard();
  const router = useRouter();
  const searchParams = useSearchParams();
  const budgets = useBudgetMeters();

  const { filters, groupBy } = useMemo(
    () => parseBoardFilters(Object.fromEntries(searchParams.entries())),
    [searchParams],
  );

  const intents = useMemo(
    () =>
      Object.values(handle.board.intents)
        .filter((intent) => intentMatchesFilters(intent, filters))
        .sort((a, b) => a.createdAtSeq - b.createdAtSeq),
    [handle.board, filters],
  );

  const candidates = useMemo(
    () =>
      Object.values(handle.board.candidates)
        .map((candidate) => ({ candidate, parent: handle.board.intents[candidate.intentId] }))
        .filter(({ candidate, parent }) => candidateMatchesFilters(candidate, parent, filters)),
    [handle.board, filters],
  );

  const options = useMemo(
    () => ({
      repos: distinctRepos(Object.values(handle.board.intents)),
      risks: [...RISK_CLASSES],
      origins: distinctOrigins(
        Object.values(handle.board.intents),
        Object.values(handle.board.candidates),
      ),
    }),
    [handle.board],
  );

  function pushQuery(nextFilters: typeof ALL_FILTERS, nextGroup: typeof groupBy): void {
    const qs = boardQueryString(nextFilters, nextGroup);
    router.replace(qs.length > 0 ? `/dashboard?${qs}` : '/dashboard', { scroll: false });
  }

  return (
    <div className="flex flex-col gap-4">
      <FirstRunRedirect />
      <ShadowBanner />
      <StaleFeedBanner lastSeq={handle.board.lastSeq} lastAdvancedAtMs={handle.lastAdvancedAtMs} />
      <BoardFilterBar
        filters={filters}
        groupBy={groupBy}
        options={options}
        onChange={(next) => pushQuery(next, groupBy)}
        onGroupChange={(mode) => pushQuery(filters, mode)}
      />
      {budgets !== null ? <BudgetStrip meters={budgets} /> : null}
      <IntentBoard
        phase={handle.phase}
        board={handle.board}
        visibleIntents={intents}
        visibleCandidates={candidates}
        errorMessage={handle.errorMessage}
        retry={handle.retry}
        groupBy={groupBy}
      />
    </div>
  );
}
