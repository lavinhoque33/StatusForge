import { useCallback, useEffect, useState } from 'react';
import { getSystem, type SystemInfo } from '../api/system';
import { isAbortError } from '../api/http';
import { describeApiError } from '../lib/errors';
import { intervalLabel } from '../lib/intervals';
import { formatLocalWithOffset } from '../lib/time';
import { usePolling } from '../lib/usePolling';

const show = (value: string | number | string[] | number[]) =>
  Array.isArray(value) ? value.join(', ') : String(value);
const time = (value: string | null) =>
  value === null ? (
    'Not yet'
  ) : (
    <time dateTime={value}>{formatLocalWithOffset(new Date(value))}</time>
  );
export function SettingsPage() {
  const [info, setInfo] = useState<SystemInfo | null>(null);
  const [error, setError] = useState('');
  const [updated, setUpdated] = useState<number | null>(null);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const next = await getSystem(signal);
      if (signal.aborted) return;
      setInfo(next);
      setUpdated(Date.now());
      setError('');
    } catch (cause) {
      if (!signal.aborted && !isAbortError(cause)) setError(describeApiError(cause));
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
  return (
    <section aria-labelledby="settings-heading">
      <h2 id="settings-heading">Settings</h2>
      {error ? (
        <p role="alert">
          {error}{' '}
          {info ? (
            'Showing last available data.'
          ) : (
            <button
              className="button"
              type="button"
              onClick={() => void load(new AbortController().signal)}
            >
              Try again
            </button>
          )}
        </p>
      ) : null}
      {!info && !error ? <p role="status">Loading settings…</p> : null}
      {info ? (
        <>
          <p role="status">
            {error ? 'Not updated' : 'Updated'}{' '}
            {updated === null ? '' : time(new Date(updated).toISOString())}
          </p>
          <section className="panel">
            <h3>Version and data format</h3>
            <dl className="config-list">
              <div>
                <dt>Version</dt>
                <dd>{info.version}</dd>
              </div>
              <div>
                <dt>Table</dt>
                <dd>{info.table}</dd>
              </div>
              <div>
                <dt>Data format</dt>
                <dd>{info.dataFormat}</dd>
              </div>
              <div>
                <dt>Written by</dt>
                <dd>{info.dataFormatWrittenBy}</dd>
              </div>
              <div>
                <dt>Upgraded from</dt>
                <dd>{info.upgradedFrom ?? 'New table'}</dd>
              </div>
            </dl>
          </section>
          <section className="panel">
            <h3>Retention</h3>
            <div
              className="retention-scroll"
              tabIndex={0}
              role="region"
              aria-label="Retention periods"
            >
              <table className="retention-table">
                <thead>
                  <tr>
                    <th scope="col">Record</th>
                    <th scope="col">Period</th>
                    <th scope="col">Counted from</th>
                    <th scope="col">Protected while</th>
                  </tr>
                </thead>
                <tbody>
                  {info.retention.map((row) => (
                    <tr key={row.record}>
                      <th scope="row">{row.label}</th>
                      <td>{row.days} days</td>
                      <td>{row.startsFrom}</td>
                      <td>{row.protectedWhile}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p>
              DynamoDB TTL on <code>{info.ttl.attribute ?? 'expiresAt'}</code>: {info.ttl.status}.
              Expired records disappear from views immediately and from storage later.
            </p>
          </section>
          <section className="panel">
            <h3>Housekeeping</h3>
            <dl className="config-list">
              <div>
                <dt>Backfill</dt>
                <dd>
                  {info.backfill.state}; {info.backfill.stamped} records stamped. Started{' '}
                  {time(info.backfill.startedAt)}; finished {time(info.backfill.finishedAt)}.
                </dd>
              </div>
              <div>
                <dt>Run interval</dt>
                <dd>{info.housekeeping.intervalSeconds} seconds</dd>
              </div>
              <div>
                <dt>Last run</dt>
                <dd>{time(info.housekeeping.lastRunAt)}</dd>
              </div>
              <div>
                <dt>Pending retention jobs</dt>
                <dd>{info.housekeeping.pendingRetentionJobs}</dd>
              </div>
              <div>
                <dt>Pending deletions</dt>
                <dd>{info.housekeeping.pendingDeletions}</dd>
              </div>
            </dl>
          </section>
          <section className="panel">
            <h3>Local limits and boundaries</h3>
            <p>
              Management and database interfaces bind to loopback only. Only configured local
              targets may be checked; this is not a hosted service.
            </p>
            <dl className="config-list">
              <div>
                <dt>Allowed targets</dt>
                <dd>{show(info.limits.allowedTargets) || 'None'}</dd>
              </div>
              <div>
                <dt>Notification URL</dt>
                <dd>{info.limits.notifyUrl || 'Not configured'}</dd>
              </div>
              <div>
                <dt>Workers</dt>
                <dd>{info.limits.workers}</dd>
              </div>
              <div>
                <dt>Minimum check interval</dt>
                <dd>{info.limits.minIntervalSeconds} seconds</dd>
              </div>
              <div>
                <dt>Delivery workers</dt>
                <dd>{info.limits.deliveryWorkers}</dd>
              </div>
              <div>
                <dt>Delivery retry schedule</dt>
                <dd>{show(info.limits.deliveryRetrySchedule)}</dd>
              </div>
              <div>
                <dt>Reminder interval</dt>
                <dd>{info.limits.reminderIntervalSeconds} seconds</dd>
              </div>
              <div>
                <dt>Liveness interval</dt>
                <dd>{info.limits.livenessIntervalSeconds} seconds</dd>
              </div>
              <div>
                <dt>Summary windows</dt>
                <dd>{info.limits.summaryWindows.map(intervalLabel).join(', ')}</dd>
              </div>
              <div>
                <dt>History scan bound</dt>
                <dd>{info.limits.historyScanBound}</dd>
              </div>
              <div>
                <dt>Summary observation limit</dt>
                <dd>{info.limits.summaryObservationLimit}</dd>
              </div>
              <div>
                <dt>Summary gap limit</dt>
                <dd>{info.limits.summaryGapLimit}</dd>
              </div>
            </dl>
          </section>
          <section className="panel">
            <h3>Export and permanent deletion</h3>
            <p>
              Export before deleting an archived monitor or application. Deletion is permanent; an
              export is the recovery path. For export, import, and deletion instructions, see{' '}
              <code>docs/operations/local-runbook.md</code>.
            </p>
          </section>
        </>
      ) : null}
    </section>
  );
}
