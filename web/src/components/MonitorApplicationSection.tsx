import { useEffect, useState } from 'react';
import { listApplications, setMembership, type Application } from '../api/applications';
import { ApiRequestError } from '../api/http';
import type { MonitorRecord } from '../api/monitors';
import { describeApiError } from '../lib/errors';

export function MonitorApplicationSection({
  monitor,
  onChange,
}: {
  monitor: MonitorRecord;
  onChange: (monitor: MonitorRecord) => void;
}) {
  const [applications, setApplications] = useState<Application[] | null>(null);
  const [selection, setSelection] = useState<string | null>(null);
  const [message, setMessage] = useState('');
  const [pending, setPending] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    void listApplications(controller.signal)
      .then(setApplications)
      .catch((cause: unknown) => {
        if (!controller.signal.aborted) setMessage(describeApiError(cause));
      });
    return () => controller.abort();
  }, []);
  const missing =
    applications !== null &&
    monitor.applicationId !== null &&
    !applications.some((app) => app.id === monitor.applicationId);
  const save = async () => {
    setPending(true);
    setMessage('');
    try {
      const updated = await setMembership(monitor.id, (selection ?? monitor.applicationId) || null);
      setSelection(null);
      onChange(updated);
      setMessage('Application updated. Configuration unchanged.');
    } catch (cause) {
      setMessage(
        cause instanceof ApiRequestError && cause.code === 'archived'
          ? 'This monitor or application is archived; membership cannot be changed.'
          : describeApiError(cause),
      );
    } finally {
      setPending(false);
    }
  };
  return (
    <section className="panel" aria-labelledby="monitor-application-heading">
      <h3 id="monitor-application-heading">Application</h3>
      <p>Membership is context only. It does not change the monitor configuration or health.</p>
      <label htmlFor="monitor-application">Application</label>
      {missing ? <p>Deleted application</p> : null}
      <select
        id="monitor-application"
        value={selection ?? monitor.applicationId ?? ''}
        disabled={
          pending ||
          applications === null ||
          monitor.lifecycle === 'archived' ||
          monitor.deletion !== null
        }
        onChange={(event) => setSelection(event.target.value)}
      >
        <option value="">No application</option>
        {missing && monitor.applicationId !== null ? (
          <option value={monitor.applicationId}>Deleted application</option>
        ) : null}
        {applications === null && monitor.applicationId !== null ? (
          <option value={monitor.applicationId}>Application list unavailable</option>
        ) : null}
        {applications
          ?.filter((app) => !app.archivedAt || app.id === monitor.applicationId)
          .map((app) => (
            <option key={app.id} value={app.id}>
              {app.name}
              {app.archivedAt ? ' (archived)' : ''}
            </option>
          ))}
      </select>
      <button
        type="button"
        className="button"
        onClick={() => void save()}
        disabled={
          pending ||
          applications === null ||
          monitor.lifecycle === 'archived' ||
          monitor.deletion !== null ||
          selection === (monitor.applicationId ?? '')
        }
      >
        Save application
      </button>
      {message ? <p role="status">{message}</p> : null}
    </section>
  );
}
