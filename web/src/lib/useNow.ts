import { useEffect, useState } from 'react';

/** Relative ages must refresh at least every 30 s. */
export const RELATIVE_AGE_REFRESH_MS = 30_000;

/** Cadence for the freshness marker (`Updated N s ago`): a text-only re-render. */
export const FRESHNESS_REFRESH_MS = 5_000;

/**
 * Current time, refreshed on a timer so time-based text stays current, plus a
 * one-shot tick at an exact deadline.
 *
 * The freshness marker (`Updated N s ago`) passes the 5 s cadence: a
 * text-only re-render, cheap enough to keep the marker honest. Everything
 * else uses the 30 s default.
 *
 * `deadlineMs` is the instant the local fresh→stale switch must happen (the
 * status's `freshUntil`): the hook re-renders at exactly that moment so the
 * headline flips to Stale without waiting for the next poll or age tick.
 * Pass null (or a past instant) to schedule nothing; the timer is cleared and
 * re-scheduled whenever the deadline changes, and on unmount.
 */
export function useNow(
  intervalMs: number = RELATIVE_AGE_REFRESH_MS,
  deadlineMs: number | null = null,
): number {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(timer);
  }, [intervalMs]);

  useEffect(() => {
    if (deadlineMs === null) return;
    const wait = deadlineMs - Date.now();
    if (wait <= 0) return;
    const timer = window.setTimeout(() => setNow(Date.now()), wait + 1);
    return () => window.clearTimeout(timer);
  }, [deadlineMs]);

  return now;
}
