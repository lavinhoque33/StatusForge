import { lazy, Suspense, useCallback, useEffect, useState } from 'react';
import { getMonitorSummary, type MonitorSummary, type SummaryWindow } from '../api/dailyUse';
import { isAbortError } from '../api/http';
import { describeApiError } from '../lib/errors';
import { coverageSentence, latencySentence, summaryNotes } from '../lib/summaryPresentation';
import { usePolling } from '../lib/usePolling';

const SummaryCharts = lazy(() => import('./SummaryCharts'));
export const SUMMARY_REFRESH_MS = 60_000;
export function HttpSummaryPanel({
  monitorId,
  createdAt,
  deadlineMs,
}: {
  monitorId: string;
  createdAt: string;
  deadlineMs?: number;
}) {
  const [window, setWindow] = useState<SummaryWindow>('24h');
  const [summary, setSummary] = useState<MonitorSummary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(
    async (signal: AbortSignal) => {
      try {
        const next = await getMonitorSummary(monitorId, window, signal);
        if (signal.aborted) return;
        setSummary(next);
        setError(null);
      } catch (failure) {
        if (!signal.aborted && !isAbortError(failure)) setError(describeApiError(failure));
      }
    },
    [monitorId, window],
  );
  useEffect(() => {
    const controller = new AbortController();
    void Promise.resolve().then(() => {
      if (!controller.signal.aborted) return load(controller.signal);
    });
    return () => controller.abort();
  }, [load]);
  const poll = useCallback(() => load(new AbortController().signal), [load]);
  usePolling({ refresh: poll, intervalMs: SUMMARY_REFRESH_MS });
  return (
    <section className="panel summary-panel" aria-labelledby="summary-heading">
      <h3 id="summary-heading">Summary</h3>
      <fieldset className="summary-window">
        <legend>Summary window</legend>
        <label>
          <input
            type="radio"
            name={`summary-window-${monitorId}`}
            checked={window === '24h'}
            onChange={() => setWindow('24h')}
          />
          24 h
        </label>
        <label>
          <input
            type="radio"
            name={`summary-window-${monitorId}`}
            checked={window === '7d'}
            onChange={() => setWindow('7d')}
          />
          7 d
        </label>
      </fieldset>
      {error === null ? null : (
        <p role="alert">
          {error}{' '}
          {summary === null ? (
            <button
              type="button"
              className="button"
              onClick={() => void load(new AbortController().signal)}
            >
              Try again
            </button>
          ) : (
            `Showing last available summary for ${summary.window}.`
          )}
        </p>
      )}
      {(summary === null || (summary.window !== window && error === null)) && error === null ? (
        <p role="status">Loading summary…</p>
      ) : null}
      {summary !== null && (summary.window === window || error !== null) ? (
        <>
          <p>{coverageSentence(summary)}</p>
          {summaryNotes(summary, createdAt).map((note) => (
            <p className="note" key={note}>
              {note}
            </p>
          ))}
          {summary.kind === 'http' ? <p>{latencySentence(summary)}</p> : null}
          <Suspense fallback={<p role="status">Loading charts…</p>}>
            <SummaryCharts summary={summary} deadlineMs={deadlineMs} />
          </Suspense>
        </>
      ) : null}
    </section>
  );
}
