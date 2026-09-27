import { useEffect, useState } from 'react';

/** Relative ages must refresh at least every 30 s. */
export const RELATIVE_AGE_REFRESH_MS = 30_000;

/** Current time, refreshed on a timer so relative ages stay current. */
export function useNow(intervalMs: number = RELATIVE_AGE_REFRESH_MS): number {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(timer);
  }, [intervalMs]);

  return now;
}
