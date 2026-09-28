import { useCallback, useEffect, useState } from 'react';
import { isAbortError } from '../api/http';
import { listMonitors, listObservations, type MonitorRecord } from '../api/monitors';
import { describeApiError } from '../lib/errors';
import { mergeActivity, type ActivityReport } from '../lib/activity';
import { notCountedReasonWords } from '../lib/reasons';
import { formatLocalWithOffset } from '../lib/time';
import { usePolling } from '../lib/usePolling';
import { Link } from '../router/Link';

export function ActivityPage() {
  const [monitors, setMonitors] = useState<MonitorRecord[]>([]);
  const [reports, setReports] = useState<ActivityReport[]>([]);
  const [filter, setFilter] = useState('all');
  const [counted, setCounted] = useState('all');
  const [error, setError] = useState<string | null>(null);
  const [updated, setUpdated] = useState<number | null>(null);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const heartbeats = (await listMonitors(signal)).filter(
        (monitor) => monitor.kind === 'heartbeat',
      );
      const lists = await Promise.all(
        heartbeats.map((monitor) => listObservations(monitor.id, 50, signal)),
      );
      if (signal.aborted) return;
      setMonitors(heartbeats);
      setReports(mergeActivity(heartbeats, lists));
      setError(null);
      setUpdated(Date.now());
    } catch (cause) {
      if (!signal.aborted && !isAbortError(cause)) setError(describeApiError(cause));
    }
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    Promise.resolve().then(() => {
      if (!controller.signal.aborted) void load(controller.signal);
    });
    return () => controller.abort();
  }, [load]);
  usePolling({ refresh: () => load(new AbortController().signal) });
  const visible = reports.filter(
    ({ monitor, observation }) =>
      (filter === 'all' || monitor.id === filter) &&
      (counted === 'all' || observation.counted === (counted === 'counted')),
  );
  return (
    <section aria-labelledby="activity-heading">
      <h2 id="activity-heading">Activity</h2>
      <p>
        Recent heartbeat reports, newest first. Up to 50 observations per heartbeat, then the 100
        newest reports across all heartbeats.
      </p>
      {error ? <p role="alert">{error}</p> : null}
      {updated === null && error === null ? <p role="status">Loading activity…</p> : null}
      {updated !== null ? (
        <p role="status">
          Updated{' '}
          <time dateTime={new Date(updated).toISOString()}>
            {formatLocalWithOffset(new Date(updated))}
          </time>
          {error ? ' — not updated' : ''}
        </p>
      ) : null}
      <div className="activity-filters">
        <label htmlFor="activity-heartbeat">Heartbeat</label>
        <select
          id="activity-heartbeat"
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
        >
          <option value="all">All heartbeats</option>
          {monitors.map((monitor) => (
            <option key={monitor.id} value={monitor.id}>
              {monitor.name}
            </option>
          ))}
        </select>
        <label htmlFor="activity-counted">Evaluation</label>
        <select
          id="activity-counted"
          value={counted}
          onChange={(event) => setCounted(event.target.value)}
        >
          <option value="all">All reports</option>
          <option value="counted">Counted</option>
          <option value="not-counted">Not counted</option>
        </select>
      </div>
      {updated !== null && visible.length === 0 ? (
        <p>No reports match these filters.</p>
      ) : (
        <ul className="activity-list">
          {visible.map(({ monitor, observation }) => (
            <li key={`${monitor.id}-${observation.id}`} className="panel">
              <Link to={`/monitors/${encodeURIComponent(monitor.id)}`}>{monitor.name}</Link> ·{' '}
              {observation.report?.late ? 'Received late' : 'Received'} ·{' '}
              {observation.counted ? (
                'Counted'
              ) : (
                <>
                  Not counted —{' '}
                  {observation.notCountedReason === 'older_than_current'
                    ? 'older than the newest report'
                    : notCountedReasonWords(observation.notCountedReason ?? '')}
                </>
              )}{' '}
              ·{' '}
              <time dateTime={observation.completedAt}>
                {formatLocalWithOffset(new Date(observation.completedAt))}
              </time>
              {observation.report?.runId ? <> · Run ID: {observation.report.runId}</> : null}
              {observation.report?.message ? <> · {observation.report.message}</> : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
