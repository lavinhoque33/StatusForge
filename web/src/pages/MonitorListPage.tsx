import { useCallback, useEffect, useRef, useState } from 'react';
import { isAbortError } from '../api/http';
import { listMonitors, type MonitorRecord } from '../api/monitors';
import { LifecycleBadge } from '../components/LifecycleBadge';
import { MonitorHeadline } from '../components/MonitorHeadline';
import { describeApiError } from '../lib/errors';
import { notUpdatedText, updatedText } from '../lib/refresh';
import { FRESHNESS_REFRESH_MS, useNow } from '../lib/useNow';
import { usePolling } from '../lib/usePolling';
import { Link } from '../router/Link';

type ListState =
  | { name: 'loading' }
  | { name: 'ready'; monitors: MonitorRecord[] }
  | { name: 'error'; message: string };

type Freshness =
  | { name: 'none' }
  | { name: 'updated'; at: number }
  | { name: 'failed'; reason: string; at: number };

/**
 * Monitor list: every lifecycle, each row presenting the monitor's current
 * status. The list polls every 15 s while the tab is visible;
 * a failed poll keeps the last data and says so.
 */
export function MonitorListPage() {
  const [state, setState] = useState<ListState>({ name: 'loading' });
  const [freshness, setFreshness] = useState<Freshness>({ name: 'none' });
  const [reloadToken, setReloadToken] = useState(0);
  // The earliest freshUntil among fresh outcome statuses: when it passes the
  // headline flips to Stale without waiting for a poll or age tick.
  const staleDeadlineMs =
    state.name === 'ready'
      ? state.monitors.reduce<number | null>((earliest, monitor) => {
          const status = monitor.status;
          const fresh =
            (status.state === 'healthy' ||
              status.state === 'failing' ||
              status.state === 'checker_problem') &&
            status.freshUntil !== null
              ? Date.parse(status.freshUntil)
              : null;
          return fresh !== null && (earliest === null || fresh < earliest) ? fresh : earliest;
        }, null)
      : null;
  const now = useNow(FRESHNESS_REFRESH_MS, staleDeadlineMs);
  const stateRef = useRef(state);

  useEffect(() => {
    stateRef.current = state;
  }, [state]);

  const load = useCallback(async (signal: AbortSignal, options: { silent: boolean }) => {
    try {
      const monitors = await listMonitors(signal);
      if (signal.aborted) return;
      setState({ name: 'ready', monitors });
      setFreshness({ name: 'updated', at: Date.now() });
    } catch (error: unknown) {
      if (signal.aborted || isAbortError(error)) return;
      if (options.silent && stateRef.current.name === 'ready') {
        // Keep the last data on the page; mark it as not updated.
        setFreshness({ name: 'failed', reason: describeApiError(error), at: Date.now() });
        return;
      }
      setState({ name: 'error', message: describeApiError(error) });
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    // Deferred to a microtask so the effect body itself performs no
    // synchronous state updates; the fetch sets state after its await.
    Promise.resolve().then(() => {
      if (!controller.signal.aborted) load(controller.signal, { silent: false });
    });
    return () => controller.abort();
  }, [load, reloadToken]);

  const poll = useCallback(async () => {
    await load(new AbortController().signal, { silent: true });
  }, [load]);

  usePolling({ refresh: poll });

  const reload = () => {
    setReloadToken((token) => token + 1);
  };

  return (
    <section aria-labelledby="monitor-list-heading">
      <h2 id="monitor-list-heading">Monitors</h2>

      {state.name === 'loading' ? <p role="status">Loading monitors…</p> : null}

      {state.name === 'ready' ? (
        <p className="updated-at" role="status">
          {freshness.name === 'failed'
            ? notUpdatedText(freshness.reason)
            : freshness.name === 'updated'
              ? updatedText(freshness.at, now)
              : null}
        </p>
      ) : null}

      {state.name === 'error' ? (
        <div className="panel">
          <p role="alert">{state.message}</p>
          <button type="button" className="button" onClick={reload}>
            Try again
          </button>
        </div>
      ) : null}

      {state.name === 'ready' && state.monitors.length === 0 ? (
        <>
          <p>No monitors yet. Create one to check the sample target.</p>
          <p>
            <Link to="/monitors/new">Create a monitor</Link>
          </p>
        </>
      ) : null}

      {state.name === 'ready' && state.monitors.length > 0 ? (
        <>
          <ul className="monitor-list">
            {state.monitors.map((monitor) => (
              <li key={monitor.id} className="monitor-card">
                <h3>
                  <Link to={`/monitors/${encodeURIComponent(monitor.id)}`}>{monitor.name}</Link>
                </h3>
                <p className="monitor-meta">
                  <LifecycleBadge lifecycle={monitor.lifecycle} />
                  <span className="version">v{monitor.configVersion}</span>
                </p>
                <MonitorHeadline
                  status={monitor.status}
                  intervalSeconds={monitor.intervalSeconds}
                  now={now}
                />
              </li>
            ))}
          </ul>
          <p>
            <Link to="/monitors/new">Create a monitor</Link>
          </p>
        </>
      ) : null}
    </section>
  );
}

export default MonitorListPage;
