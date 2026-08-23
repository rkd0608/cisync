'use client';

// Client-side ledger sync. WHY polling the /v1/events tail instead of a list
// endpoint: none exists in the v1 contract (openapi.yaml); the event stream is
// the documented sync surface for web consumers. A 416 means our after_seq
// cursor predates retention — we reset to 0 and replay (dedupe by envelope id
// makes replay safe).
import { useCallback, useEffect, useRef, useState } from 'react';
import { getEvents } from './sauron-api';
import { isBadCursor } from './sauron-api';
import {
  applyEvents,
  emptyBoard,
  type BoardState,
} from './event-board';

export const BOARD_EVENT_TYPES = [
  'intent.declared',
  'candidate.submitted',
  'decision.rendered',
] as const;

export const POLL_INTERVAL_MS = 3000;
const PAGE_LIMIT = 500;

export interface EventBoardHandle {
  phase: 'loading' | 'ready' | 'error';
  board: BoardState;
  errorMessage: string | null;
  retry: () => void;
}

export function useEventBoard(): EventBoardHandle {
  const [board, setBoard] = useState<BoardState>(emptyBoard);
  const [phase, setPhase] = useState<EventBoardHandle['phase']>('loading');
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  // Cursor + board live in refs so the poll loop reads fresh values without
  // re-subscribing on every tick.
  const cursorRef = useRef(0);
  const boardRef = useRef<BoardState>(emptyBoard());
  const stoppedRef = useRef(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const poll = useCallback(async () => {
    const result = await getEvents({
      afterSeq: cursorRef.current,
      types: [...BOARD_EVENT_TYPES],
      limit: PAGE_LIMIT,
    });

    if (stoppedRef.current) return;

    if (isBadCursor(result)) {
      // Reset to seq 0; applyEvents dedupes by envelope id, so replay is safe.
      cursorRef.current = 0;
      boardRef.current = emptyBoard();
      setBoard(boardRef.current);
      scheduleNext();
      return;
    }

    if (!result.ok) {
      setPhase('error');
      setErrorMessage(result.message);
      return;
    }

    boardRef.current = applyEvents(boardRef.current, result.data.events);
    cursorRef.current = Math.max(cursorRef.current, result.data.nextSeq);
    setBoard(boardRef.current);
    setPhase('ready');
    setErrorMessage(null);
    scheduleNext();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function scheduleNext(): void {
    if (stoppedRef.current) return;
    timerRef.current = setTimeout(() => void poll(), POLL_INTERVAL_MS);
  }

  useEffect(() => {
    stoppedRef.current = false;
    void poll();
    return () => {
      stoppedRef.current = true;
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [poll]);

  const retry = useCallback(() => {
    setPhase('loading');
    setErrorMessage(null);
    void poll();
  }, [poll]);

  return { phase, board, errorMessage, retry };
}
