import { useCallback, useEffect, useRef, useState } from 'react';
import {
  ATTENTION_LIMIT,
  incidentLink,
  listAttention,
  listIncidents,
  type AttentionNotification,
  type Incident,
} from '../api/incidents';
import { isAbortError } from '../api/http';
import { describeApiError } from '../lib/errors';
import { durationWords, resolutionWords } from '../lib/incidentPresentation';
import { formatLocalWithOffset, formatRelativeAge } from '../lib/time';
import { useNow } from '../lib/useNow';
import { usePolling } from '../lib/usePolling';
import { Link } from '../router/Link';

export function IncidentsPage() {
  const [incidents, setIncidents] = useState<Incident[] | null>(null);
  const [attention, setAttention] = useState<AttentionNotification[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [updated, setUpdated] = useState<number | null>(null);
  const initialHashHandled = useRef(false);
  const now = useNow(1000);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const [nextIncidents, nextAttention] = await Promise.all([
        listIncidents('all', 50, signal),
        listAttention(ATTENTION_LIMIT, signal),
      ]);
      if (signal.aborted) return;
      setIncidents(nextIncidents);
      setAttention(nextAttention);
      setUpdated(Date.now());
      setError(null);
    } catch (failure) {
      if (!signal.aborted && !isAbortError(failure)) setError(describeApiError(failure));
    }
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    void Promise.resolve().then(() => {
      if (!controller.signal.aborted) return load(controller.signal);
    });
    return () => controller.abort();
  }, [load]);
  usePolling({ refresh: () => load(new AbortController().signal) });
  useEffect(() => {
    if (updated === null || initialHashHandled.current) return;
    initialHashHandled.current = true;
    if (window.location.hash !== '#notifications-attention') return;
    const heading = document.getElementById('attention-list-heading');
    heading?.scrollIntoView();
    heading?.focus();
  }, [updated]);
  const open = incidents?.filter((incident) => incident.state === 'open') ?? [];
  const resolved = incidents?.filter((incident) => incident.state === 'resolved') ?? [];
  return (
    <section aria-labelledby="incidents-heading">
      <h2 id="incidents-heading">Incidents</h2>
      {error === null ? null : (
        <p role="alert">
          {error}{' '}
          {incidents === null ? (
            <button
              className="button"
              type="button"
              onClick={() => void load(new AbortController().signal)}
            >
              Try again
            </button>
          ) : (
            'Showing last available data.'
          )}
        </p>
      )}
      {updated === null && error === null ? <p role="status">Loading incidents…</p> : null}
      {updated === null ? null : (
        <p role="status">Updated {formatRelativeAge(new Date(updated).toISOString(), now)}</p>
      )}
      <section aria-labelledby="open-incidents-heading">
        <h3 id="open-incidents-heading">Open incidents</h3>
        {incidents !== null && open.length === 0 ? <p>No open incidents.</p> : null}
        <ul className="incident-list">
          {open.map((incident) => (
            <li key={incident.id} className="panel">
              <h4>
                <Link to={incidentLink(incident.monitorId, incident.id)}>
                  {incident.monitorName} — Open incident
                </Link>
              </h4>
              <p>
                Opened{' '}
                <time dateTime={incident.openedAt}>
                  {formatLocalWithOffset(new Date(incident.openedAt))}
                </time>{' '}
                ({formatRelativeAge(incident.openedAt, now)}). {incident.failureCount} failed
                checks.
              </p>
              {incident.monitoringPaused ? <p>Monitoring paused</p> : null}
              {incident.inMaintenance ? <p>In maintenance</p> : null}
              <p>{incident.maintenanceObservationCount} observations during maintenance.</p>
              <p>
                Notifications: {incident.notificationSummary.delivered} delivered ·{' '}
                {incident.notificationSummary.pending} pending ·{' '}
                {incident.notificationSummary.failed} failed
              </p>
            </li>
          ))}
        </ul>
      </section>
      <section aria-labelledby="resolved-incidents-heading">
        <h3 id="resolved-incidents-heading">Resolved incidents</h3>
        {incidents !== null && resolved.length === 0 ? <p>No resolved incidents.</p> : null}
        <ul className="incident-list">
          {resolved.map((incident) => (
            <li key={incident.id} className="panel">
              <h4>
                <Link to={incidentLink(incident.monitorId, incident.id)}>
                  {incident.monitorName} — {resolutionWords(incident.resolution)}
                </Link>
              </h4>
              <p>
                Opened{' '}
                <time dateTime={incident.openedAt}>
                  {formatLocalWithOffset(new Date(incident.openedAt))}
                </time>
                ; duration {durationWords(incident, now)}. {incident.failureCount} failed checks.
              </p>
              <p>{incident.maintenanceObservationCount} observations during maintenance.</p>
              <p>
                Notifications: {incident.notificationSummary.delivered} delivered ·{' '}
                {incident.notificationSummary.pending} pending ·{' '}
                {incident.notificationSummary.failed} failed
              </p>
            </li>
          ))}
        </ul>
      </section>
      <section id="notifications-attention" tabIndex={-1} aria-labelledby="attention-list-heading">
        <h3 id="attention-list-heading" tabIndex={-1}>
          Failed notifications needing attention
          {attention?.length === ATTENTION_LIMIT ? ` (${ATTENTION_LIMIT}+)` : null}
        </h3>
        {attention !== null && attention.length === 0 ? <p>No failed notifications.</p> : null}
        {attention === null && error === null ? <p>Loading notifications…</p> : null}
        <ul className="incident-list">
          {attention?.map((note) => (
            <li key={`${note.monitorId}:${note.id}`} className="panel">
              <Link to={`${incidentLink(note.monitorId, note.incidentId)}#notifications`}>
                {note.monitorName} — {note.kind} notification
              </Link>
              {note.failedAt === null ? null : (
                <>
                  {' '}
                  failed{' '}
                  <time dateTime={note.failedAt}>
                    {formatLocalWithOffset(new Date(note.failedAt))}
                  </time>
                </>
              )}
            </li>
          ))}
        </ul>
      </section>
    </section>
  );
}
