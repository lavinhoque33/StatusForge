import { useEffect, useState } from 'react';
import { isAbortError } from '../api/http';
import { listMonitors, type MonitorListItem } from '../api/monitors';
import { LifecycleBadge } from '../components/LifecycleBadge';
import { MonitorHeadline } from '../components/MonitorHeadline';
import { describeApiError } from '../lib/errors';
import { useNow } from '../lib/useNow';
import { Link } from '../router/Link';

type ListState =
  | { name: 'loading' }
  | { name: 'ready'; monitors: MonitorListItem[] }
  | { name: 'error'; message: string };

/** Monitor list: every lifecycle, each row reduced to its newest manual check. */
export function MonitorListPage() {
  const [state, setState] = useState<ListState>({ name: 'loading' });
  const [reloadToken, setReloadToken] = useState(0);
  const now = useNow();

  useEffect(() => {
    const controller = new AbortController();
    listMonitors(controller.signal)
      .then((monitors) => {
        if (controller.signal.aborted) return;
        setState({ name: 'ready', monitors });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted || isAbortError(error)) return;
        setState({ name: 'error', message: describeApiError(error) });
      });
    return () => controller.abort();
  }, [reloadToken]);

  const reload = () => {
    setState({ name: 'loading' });
    setReloadToken((token) => token + 1);
  };

  return (
    <section aria-labelledby="monitor-list-heading">
      <h2 id="monitor-list-heading">Monitors</h2>

      {state.name === 'loading' ? <p role="status">Loading monitors…</p> : null}

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
                <MonitorHeadline lastObservation={monitor.lastObservation} now={now} />
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
