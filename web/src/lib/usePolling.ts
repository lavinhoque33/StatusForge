/**
 * Interval polling: poll while the tab is visible,
 * stop while hidden, and refresh once on becoming visible.
 *
 * The interval keeps running and checks `document.visibilityState`; a hidden
 * tab fires no requests, and the visibilitychange listener triggers one
 * immediate refresh when the tab becomes visible again. Failed polls are the
 * caller's concern: this hook only reports success or an error.
 */
import { useEffect, useEffectEvent } from 'react';

/** Poll cadence for list and detail. */
export const POLL_INTERVAL_MS = 15_000;

type PollOptions = {
  /** One poll; may reject, which the caller reports without losing data. */
  refresh: () => Promise<unknown>;
  /** Fire an immediate poll on mount instead of waiting one interval. */
  immediate?: boolean;
  /** Independent refresh cadence for summaries; defaults to the normal 15 s poll. */
  intervalMs?: number;
};

/**
 * Run `refresh` every 15 s while visible. The callback is read through an
 * effect event, so callers may pass an inline closure without rescheduling the
 * timer.
 */
export function usePolling({
  refresh,
  immediate = false,
  intervalMs = POLL_INTERVAL_MS,
}: PollOptions): void {
  const runRefresh = useEffectEvent(() => refresh());

  useEffect(() => {
    let inFlight = false;
    let pending = false;

    const visible = () => document.visibilityState === 'visible';

    const run = () => {
      if (!visible() || inFlight) {
        return;
      }
      inFlight = true;
      Promise.resolve()
        .then(runRefresh)
        .catch(() => {
          // The caller renders the failure; the loop keeps its cadence.
        })
        .finally(() => {
          inFlight = false;
          if (pending) {
            pending = false;
            run();
          }
        });
    };

    const onVisibilityChange = () => {
      if (visible()) {
        // Refresh once on becoming visible; the data may be a poll old.
        run();
      }
    };

    document.addEventListener('visibilitychange', onVisibilityChange);
    if (immediate) {
      run();
    }
    const timer = window.setInterval(run, intervalMs);
    return () => {
      document.removeEventListener('visibilitychange', onVisibilityChange);
      window.clearInterval(timer);
    };
  }, [immediate, intervalMs]);
}
