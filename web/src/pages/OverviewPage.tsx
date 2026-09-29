import { useCallback, useEffect, useState } from 'react';
import { listApplications, type Application } from '../api/applications';
import { getOverview, type Overview } from '../api/dailyUse';
import { incidentLink } from '../api/incidents';
import { isAbortError } from '../api/http';
import { groupByApplication } from '../lib/applicationGroups';
import { describeApiError } from '../lib/errors';
import { durationWords } from '../lib/incidentPresentation';
import { formatLocalWithOffset, formatRelativeAge } from '../lib/time';
import { useNow } from '../lib/useNow';
import { usePolling } from '../lib/usePolling';
import { Link } from '../router/Link';

function When({ at }: { at: string }) {
  return <time dateTime={at}>{formatLocalWithOffset(new Date(at))}</time>;
}
export function OverviewPage() {
  const [data, setData] = useState<Overview | null>(null);
  const [applications, setApplications] = useState<Application[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<number | null>(null);
  const now = useNow(1000);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const [result, apps] = await Promise.all([getOverview(signal), listApplications(signal)]);
      if (signal.aborted) return;
      setData(result);
      setApplications(apps);
      setUpdatedAt(Date.now());
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
  const applicationNames = new Map(applications.map((app) => [app.id, app.name]));
  return (
    <section className="overview" aria-labelledby="overview-heading">
      <h2 id="overview-heading">Overview</h2>
      {error !== null ? (
        <p role="alert">
          {error}{' '}
          {data === null ? (
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
      ) : null}
      {data === null && error === null ? <p role="status">Loading overview…</p> : null}
      {updatedAt !== null ? (
        <p role="status">Updated {formatRelativeAge(new Date(updatedAt).toISOString(), now)}</p>
      ) : null}
      {data === null ? null : (
        <>
          {data.openIncidents.length === 0 &&
          data.failingWithoutIncident.length === 0 &&
          data.coverageProblems.length === 0 &&
          data.notifications.count === 0 &&
          data.scheduler.state !== 'behind' ? (
            <p className="panel">
              All clear — {data.counts.active} active monitor{data.counts.active === 1 ? '' : 's'}{' '}
              checked recently; {data.counts.paused} paused. Evaluated{' '}
              <When at={data.evaluatedAt} />.
            </p>
          ) : null}
          {data.scheduler.state === 'disabled' ? (
            <p className="note">
              Scheduled checks are turned off (<code>STATUSFORGE_SCHEDULER_ENABLED=false</code>);
              only manual checks run.
            </p>
          ) : null}
          {data.receiveOutages.length > 0 ? (
            <section aria-labelledby="outages-heading">
              <h3 id="outages-heading">StatusForge was not receiving</h3>
              <ul className="overview-list">
                {data.receiveOutages.map((outage) => (
                  <li className="panel" key={outage.from}>
                    From <When at={outage.from} /> to{' '}
                    {outage.to === null ? 'now' : <When at={outage.to} />} (
                    {Math.round(
                      ((outage.to === null ? Date.parse(data.evaluatedAt) : Date.parse(outage.to)) -
                        Date.parse(outage.from)) /
                        60000,
                    )}{' '}
                    min); checks and reports during this time were not observed.
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {data.scheduler.state === 'behind' ? (
            <section aria-labelledby="scheduler-behind-heading">
              <h3 id="scheduler-behind-heading">Scheduler is behind</h3>
              <div className="panel">
                <p>
                  Scheduler is behind — {data.scheduler.missedChecks} of {data.scheduler.dueChecks}{' '}
                  due check{data.scheduler.dueChecks === 1 ? '' : 's'} in the last{' '}
                  {data.scheduler.windowMinutes} minutes were missed because workers were busy.
                  Missed checks are recorded as coverage gaps.
                </p>
                <p>
                  To keep up, use longer check intervals or monitor fewer targets. If checks wait on
                  slow targets, raising <code>STATUSFORGE_WORKERS</code> (now{' '}
                  {data.scheduler.workers}; up to 16) can also help.
                </p>
              </div>
            </section>
          ) : null}
          {data.openIncidents.length > 0 ? (
            <section aria-labelledby="overview-incidents-heading">
              <h3 id="overview-incidents-heading">Open incidents</h3>
              {groupByApplication(data.openIncidents, applicationNames).map((group) => (
                <section key={group.name}>
                  <h4>{group.name}</h4>
                  <ul className="overview-list">
                    {group.items.map(({ monitor, incident }) => (
                      <li className="panel" key={incident.id}>
                        <Link to={incidentLink(monitor.id, incident.id)}>
                          {monitor.name} —{' '}
                          {monitor.kind === 'heartbeat' ? 'heartbeat' : 'HTTP check'} incident
                        </Link>
                        . Opened <When at={incident.openedAt} />; duration{' '}
                        {durationWords(incident, now)}.{' '}
                        {incident.lastFailure.reason.replaceAll('_', ' ')}.
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </section>
          ) : null}
          {data.failingWithoutIncident.length > 0 ? (
            <section aria-labelledby="failing-heading">
              <h3 id="failing-heading">Failing — incident not open yet</h3>
              {groupByApplication(data.failingWithoutIncident, applicationNames).map((group) => (
                <section key={group.name}>
                  <h4>{group.name}</h4>
                  <ul className="overview-list">
                    {group.items.map(({ monitor }) => (
                      <li className="panel monitor-state--failing" key={monitor.id}>
                        <Link to={`/monitors/${encodeURIComponent(monitor.id)}`}>
                          {monitor.name}
                        </Link>{' '}
                        — Failing; policy threshold not reached yet.
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </section>
          ) : null}
          {data.coverageProblems.length > 0 ? (
            <section aria-labelledby="coverage-problems-heading">
              <h3 id="coverage-problems-heading">Coverage problems</h3>
              {groupByApplication(data.coverageProblems, applicationNames).map((group) => (
                <section key={group.name}>
                  <h4>{group.name}</h4>
                  <ul className="overview-list">
                    {group.items.map(({ monitor, status }) => (
                      <li className={`panel monitor-state--${status.state}`} key={monitor.id}>
                        <Link to={`/monitors/${encodeURIComponent(monitor.id)}`}>
                          {monitor.name}
                        </Link>{' '}
                        —{' '}
                        {status.state === 'stale' ? (
                          <>
                            Stale — not checked since{' '}
                            {status.observation === null ? (
                              'the last recorded check'
                            ) : (
                              <When at={status.observation.completedAt} />
                            )}
                          </>
                        ) : status.state === 'checker_problem' ? (
                          'Checker problem — StatusForge could not complete the check — not a target failure'
                        ) : status.state === 'late' ? (
                          'Late — expected report not yet received'
                        ) : (
                          'Unknown — no current result'
                        )}
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </section>
          ) : null}
          {data.notifications.count > 0 ? (
            <section aria-labelledby="overview-notifications-heading">
              <h3 id="overview-notifications-heading">Notifications need attention</h3>
              <p>
                {data.notifications.count} failed notification
                {data.notifications.count === 1 ? '' : 's'} (newest {data.limits.notifications}{' '}
                shown).
              </p>
              {groupByApplication(data.notifications.items, applicationNames).map((group) => (
                <section key={group.name}>
                  <h4>{group.name}</h4>
                  <ul className="overview-list">
                    {group.items.map((note) => (
                      <li className="panel" key={note.id}>
                        <Link to={incidentLink(note.monitorId, note.incidentId)}>
                          {note.monitorName} — {note.kind} notification
                        </Link>{' '}
                        failed
                        {note.failedAt === null ? null : (
                          <>
                            {' '}
                            at <When at={note.failedAt} />
                          </>
                        )}
                        .
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </section>
          ) : null}
          {data.recentRecoveries.length > 0 ? (
            <section aria-labelledby="recoveries-heading">
              <h3 id="recoveries-heading">Recovered in the last 24 hours</h3>
              {groupByApplication(data.recentRecoveries, applicationNames).map((group) => (
                <section key={group.name}>
                  <h4>{group.name}</h4>
                  <ul className="overview-list">
                    {group.items.map(({ monitor, incident }) => (
                      <li className="panel" key={incident.id}>
                        <Link to={incidentLink(monitor.id, incident.id)}>
                          {monitor.name} — Recovered
                        </Link>{' '}
                        at{' '}
                        {incident.resolvedAt === null ? (
                          'unknown time'
                        ) : (
                          <When at={incident.resolvedAt} />
                        )}
                        ; incident duration {durationWords(incident, now)}.
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </section>
          ) : null}
          <p className="note">
            Up to {data.limits.openIncidents} open incidents, {data.limits.recentRecoveries}{' '}
            recoveries in {data.limits.recoveryWindowHours} hours, and {data.limits.notifications}{' '}
            notifications shown.
          </p>
        </>
      )}
    </section>
  );
}
