import { useCallback, useEffect, useRef, useState } from 'react';
import {
  getIncident,
  retryNotification,
  type Evidence,
  type IncidentDetail,
  type Notification,
} from '../api/incidents';
import { ApiRequestError, isAbortError } from '../api/http';
import { describeApiError } from '../lib/errors';
import {
  attemptResultWords,
  durationWords,
  evidenceWords,
  mergeIncidentTimeline,
  resolutionWords,
  timelineWords,
} from '../lib/incidentPresentation';
import { formatLocalWithOffset } from '../lib/time';
import { useNow } from '../lib/useNow';
import { usePolling } from '../lib/usePolling';
import { Link } from '../router/Link';

function EvidenceList({ items }: { items: Evidence[] }) {
  return (
    <ul>
      {items.map((item) => (
        <li key={item.observationId}>
          <time dateTime={item.startedAt}>{formatLocalWithOffset(new Date(item.startedAt))}</time>:{' '}
          {evidenceWords(item)}
        </li>
      ))}
    </ul>
  );
}

export function IncidentDetailPage({
  monitorId,
  incidentId,
}: {
  monitorId: string;
  incidentId: string;
}) {
  const [detail, setDetail] = useState<IncidentDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [pendingId, setPendingId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const rowHeadings = useRef<Map<string, HTMLHeadingElement>>(new Map());
  const focusAfterRetry = useRef<string | null>(null);
  const now = useNow(1000);
  const load = useCallback(
    async (signal: AbortSignal) => {
      try {
        const next = await getIncident(monitorId, incidentId, signal);
        if (signal.aborted) return;
        setDetail(next);
        setError(null);
        setNotFound(false);
      } catch (failure) {
        if (signal.aborted || isAbortError(failure)) return;
        if (failure instanceof ApiRequestError && failure.code === 'incident_not_found')
          setNotFound(true);
        else setError(describeApiError(failure));
      }
    },
    [monitorId, incidentId],
  );
  useEffect(() => {
    const controller = new AbortController();
    void Promise.resolve().then(() => {
      if (!controller.signal.aborted) return load(controller.signal);
    });
    return () => controller.abort();
  }, [load]);
  usePolling({ refresh: () => load(new AbortController().signal) });
  useEffect(() => {
    if (pendingId !== null || focusAfterRetry.current === null) return;
    const target =
      rowHeadings.current.get(focusAfterRetry.current) ??
      document.getElementById('notifications-heading');
    target?.focus();
    if (document.activeElement === target) focusAfterRetry.current = null;
  }, [detail, pendingId]);
  const retry = async (note: Notification) => {
    if (pendingId !== null || note.state !== 'failed') return;
    setPendingId(note.id);
    setNotice(null);
    try {
      const next = await retryNotification(monitorId, incidentId, note.id);
      setDetail((current) =>
        current === null
          ? null
          : {
              ...current,
              notifications: current.notifications.map((item) =>
                item.id === note.id ? next : item,
              ),
            },
      );
      setNotice('Retry queued. Delivery will be attempted once.');
    } catch (failure) {
      if (failure instanceof ApiRequestError && failure.code === 'not_failed') {
        setNotice('Notification changed and is no longer failed. Details reloaded.');
        await load(new AbortController().signal);
      } else setNotice(describeApiError(failure));
    } finally {
      focusAfterRetry.current = note.id;
      setPendingId(null);
    }
  };
  if (notFound)
    return (
      <section>
        <h2>Incident not found</h2>
        <p>No incident exists at this address.</p>
        <Link to="/incidents">Back to incidents</Link>
      </section>
    );
  if (detail === null)
    return (
      <section>
        <h2>Incident</h2>
        {error === null ? (
          <p role="status">Loading incident…</p>
        ) : (
          <p role="alert">
            {error}{' '}
            <button
              className="button"
              type="button"
              onClick={() => void load(new AbortController().signal)}
            >
              Try again
            </button>
          </p>
        )}
      </section>
    );
  const { incident, events, gaps, notifications } = detail;
  return (
    <article aria-labelledby="incident-heading">
      <h2 id="incident-heading">
        {incident.monitorName} —{' '}
        {incident.state === 'open' ? 'Open incident' : resolutionWords(incident.resolution)}
      </h2>
      <p>
        <Link to={`/monitors/${encodeURIComponent(monitorId)}`}>Back to monitor</Link> ·{' '}
        <Link to="/incidents">All incidents</Link>
      </p>
      <p>
        Opened{' '}
        <time dateTime={incident.openedAt}>
          {formatLocalWithOffset(new Date(incident.openedAt))}
        </time>
        ; {incident.state === 'open' ? 'open for' : 'duration'} {durationWords(incident, now)}.
      </p>
      {incident.resolvedAt === null ? null : (
        <p>
          Resolved{' '}
          <time dateTime={incident.resolvedAt}>
            {formatLocalWithOffset(new Date(incident.resolvedAt))}
          </time>{' '}
          — {resolutionWords(incident.resolution)}
        </p>
      )}
      {incident.monitoringPaused ? <p>Monitoring paused</p> : null}
      {incident.inMaintenance ? <p>In maintenance</p> : null}
      {error === null ? null : (
        <p role="alert">Could not refresh: {error}. Showing last available data.</p>
      )}
      <section className="panel" aria-labelledby="opening-heading">
        <h3 id="opening-heading">Opening evidence</h3>
        <EvidenceList items={incident.openingEvidence} />
      </section>
      <section className="panel" aria-labelledby="failure-heading">
        <h3 id="failure-heading">Failure summary</h3>
        <p>
          {incident.failureCount} failed checks between{' '}
          <time dateTime={incident.firstFailureAt}>
            {formatLocalWithOffset(new Date(incident.firstFailureAt))}
          </time>{' '}
          and{' '}
          <time dateTime={incident.lastFailure.startedAt}>
            {formatLocalWithOffset(new Date(incident.lastFailure.startedAt))}
          </time>
          .
        </p>
        <p>Last failure: {evidenceWords(incident.lastFailure)}</p>
        <p>
          {incident.checkerProblemCount} checker problems
          {incident.lastCheckerProblem === null
            ? '.'
            : `; last at ${formatLocalWithOffset(new Date(incident.lastCheckerProblem.startedAt))}: ${evidenceWords(incident.lastCheckerProblem)}`}
        </p>
        {incident.maintenanceObservationCount > 0 ? (
          <p>{incident.maintenanceObservationCount} observations during maintenance.</p>
        ) : null}
      </section>
      <section className="panel" aria-labelledby="incident-timeline-heading">
        <h3 id="incident-timeline-heading">Timeline</h3>
        {events.length === 0 && gaps.length === 0 ? (
          <p>No timeline events recorded.</p>
        ) : (
          <ol className="incident-timeline">
            {mergeIncidentTimeline(events, gaps).map((item, index) => (
              <li key={`${item.kind}:${item.at}:${index}`}>
                <time dateTime={item.at}>{formatLocalWithOffset(new Date(item.at))}</time> —{' '}
                {timelineWords(item)}
              </li>
            ))}
          </ol>
        )}
      </section>
      {incident.recoveryEvidence.length === 0 ? null : (
        <section className="panel" aria-labelledby="recovery-heading">
          <h3 id="recovery-heading">Recovery evidence</h3>
          <EvidenceList items={incident.recoveryEvidence} />
        </section>
      )}
      <section
        id="notifications"
        tabIndex={-1}
        className="panel"
        aria-labelledby="notifications-heading"
      >
        <h3 id="notifications-heading" tabIndex={-1}>
          Notifications
        </h3>
        {notice === null ? null : <p role="status">{notice}</p>}
        {notifications.length === 0 ? (
          <p>No notifications recorded.</p>
        ) : (
          <ul className="incident-list">
            {notifications.map((note) => (
              <li key={note.id} className="notification-card">
                <h4
                  tabIndex={-1}
                  ref={(node) => {
                    if (node) rowHeadings.current.set(note.id, node);
                    else rowHeadings.current.delete(note.id);
                  }}
                >
                  {note.kind === 'reminder'
                    ? `Reminder ${note.reminderSeq ?? ''}`
                    : note.kind === 'opened'
                      ? 'Opened'
                      : 'Resolved'}{' '}
                  — {note.state.replace('_', ' ')}
                </h4>
                <p>
                  Created{' '}
                  <time dateTime={note.createdAt}>
                    {formatLocalWithOffset(new Date(note.createdAt))}
                  </time>
                  {note.nextAttemptAt === null
                    ? ''
                    : `; next attempt ${formatLocalWithOffset(new Date(note.nextAttemptAt))}`}
                  {note.cancelledReason === null ? '' : '; cancelled because the incident resolved'}
                </p>
                {note.attempts.length === 0 ? (
                  <p>No delivery attempts yet.</p>
                ) : (
                  <ol>
                    {note.attempts.map((attempt) => (
                      <li key={attempt.number}>
                        Attempt {attempt.number}:{' '}
                        <time dateTime={attempt.startedAt}>
                          {formatLocalWithOffset(new Date(attempt.startedAt))}
                        </time>{' '}
                        — {attemptResultWords(attempt)}
                        {attempt.httpStatus === null ||
                        attempt.result === 'http_error' ||
                        attempt.result === 'rejected'
                          ? ''
                          : ` · HTTP ${attempt.httpStatus}`}
                        {attempt.durationMs === null ? '' : ` · ${attempt.durationMs} ms`}
                        {attempt.manual ? ' · Manual retry' : ''}
                      </li>
                    ))}
                  </ol>
                )}
                {note.state === 'failed' ? (
                  <button
                    type="button"
                    className="button"
                    aria-disabled={pendingId === note.id ? true : undefined}
                    onClick={() => void retry(note)}
                  >
                    {pendingId === note.id ? 'Retrying…' : 'Retry delivery'}
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>
    </article>
  );
}
