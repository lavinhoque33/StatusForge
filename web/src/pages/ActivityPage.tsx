import { useCallback, useEffect, useState } from 'react';
import { isAbortError } from '../api/http';
import { listMonitors, listObservations, type MonitorRecord } from '../api/monitors';
import { listApplications, listMarkers, type Application } from '../api/applications';
import { ApplicationReference } from '../components/ApplicationReference';
import { describeApiError } from '../lib/errors';
import { mergeActivity, mergeActivityItems, type ActivityItem } from '../lib/activity';
import { notCountedReasonWords } from '../lib/reasons';
import { formatLocalWithOffset } from '../lib/time';
import { usePolling } from '../lib/usePolling';
import { Link } from '../router/Link';

export function ActivityPage() {
  const [monitors, setMonitors] = useState<MonitorRecord[]>([]);
  const [items, setItems] = useState<ActivityItem[]>([]);
  const [applications, setApplications] = useState<Application[]>([]);
  const [applicationFilter, setApplicationFilter] = useState('all');
  const [filter, setFilter] = useState('all');
  const [counted, setCounted] = useState('all');
  const [error, setError] = useState<string | null>(null);
  const [updated, setUpdated] = useState<number | null>(null);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const [allMonitors, allApplications] = await Promise.all([
        listMonitors(signal),
        listApplications(signal),
      ]);
      const heartbeats = allMonitors.filter((monitor) => monitor.kind === 'heartbeat');
      const [lists, markers] = await Promise.all([
        Promise.all(heartbeats.map((monitor) => listObservations(monitor.id, 50, signal))),
        Promise.all(allApplications.map((app) => listMarkers(app.id, signal))),
      ]);
      if (signal.aborted) return;
      setMonitors(heartbeats);
      setApplications(allApplications);
      setItems(mergeActivityItems(mergeActivity(heartbeats, lists), allApplications, markers));
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
  const visible = items.filter((item) =>
    item.kind === 'deployment'
      ? filter === 'all' &&
        counted === 'all' &&
        (applicationFilter === 'all' || item.application.id === applicationFilter)
      : (filter === 'all' || item.monitor.id === filter) &&
        (counted === 'all' || item.observation.counted === (counted === 'counted')) &&
        (applicationFilter === 'all' || item.monitor.applicationId === applicationFilter),
  );
  return (
    <section aria-labelledby="activity-heading">
      <h2 id="activity-heading">Activity</h2>
      <p>
        Recent heartbeat reports and deployments, newest first. Up to 50 observations per heartbeat
        and 50 markers per application, then the 100 newest entries.
      </p>
      <p className="note">Deployment markers are kept for 365 days.</p>
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
        <label htmlFor="activity-application">Application</label>
        <select
          id="activity-application"
          value={applicationFilter}
          onChange={(event) => setApplicationFilter(event.target.value)}
        >
          <option value="all">All applications</option>
          {applications.map((application) => (
            <option key={application.id} value={application.id}>
              {application.name}
              {application.archivedAt ? ' (archived)' : ''}
            </option>
          ))}
        </select>
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
        <p>No activity matches these filters.</p>
      ) : (
        <ul className="activity-list">
          {visible.map((item) =>
            item.kind === 'deployment' ? (
              <li key={`deployment-${item.marker.id}`} className="panel">
                <Link to="/applications">{item.application.name}</Link> · Deployment{' '}
                {item.marker.version} ·{' '}
                <time dateTime={item.marker.reportedAt}>
                  {formatLocalWithOffset(new Date(item.marker.reportedAt))}
                </time>
                {item.marker.description ? <> · {item.marker.description}</> : null}
              </li>
            ) : (
              <li key={`${item.monitor.id}-${item.observation.id}`} className="panel">
                <Link to={`/monitors/${encodeURIComponent(item.monitor.id)}`}>
                  {item.monitor.name}
                </Link>{' '}
                {item.monitor.applicationId !== null ? (
                  <>
                    Application:{' '}
                    <ApplicationReference
                      id={item.monitor.applicationId}
                      applications={applications}
                    />{' '}
                  </>
                ) : null}
                · {item.observation.report?.late ? 'Received late' : 'Received'} ·{' '}
                {item.observation.counted ? (
                  'Counted'
                ) : (
                  <>
                    Not counted —{' '}
                    {item.observation.notCountedReason === 'older_than_current'
                      ? 'older than the newest report'
                      : notCountedReasonWords(item.observation.notCountedReason ?? '')}
                  </>
                )}
                {' · '}
                <time dateTime={item.observation.completedAt}>
                  {formatLocalWithOffset(new Date(item.observation.completedAt))}
                </time>
                {item.observation.report?.runId ? (
                  <> · Run ID: {item.observation.report.runId}</>
                ) : null}
                {item.observation.report?.message ? (
                  <> · {item.observation.report.message}</>
                ) : null}
              </li>
            ),
          )}
        </ul>
      )}
    </section>
  );
}
