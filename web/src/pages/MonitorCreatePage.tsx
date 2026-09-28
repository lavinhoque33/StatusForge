import { useEffect, useRef, useState } from 'react';
import { ApiValidationError, isAbortError, type FieldIssue } from '../api/http';
import { createMonitor, listIntervals, type Intervals } from '../api/monitors';
import { MonitorForm } from '../components/MonitorForm';
import { HeartbeatTokenPanel } from '../components/HeartbeatTokenPanel';
import { describeApiError } from '../lib/errors';
import { intervalChoice, type IntervalChoice } from '../lib/intervalChoice';
import {
  DEFAULT_MONITOR_FIELDS,
  MONITOR_FIELD_PATHS,
  checkInputFromFields,
  incidentPolicyFromFields,
  splitFieldErrors,
  type MonitorFormFields,
} from '../lib/monitorForm';
import { navigate } from '../router/history';
import { Link } from '../router/Link';

/**
 * Create form for a new monitor. The interval defaults to the
 * backend's `defaultIntervalSeconds` once `/api/intervals` answers; submitting
 * without an answer omits `intervalSeconds`, so the backend applies its own
 * default.
 */
export function MonitorCreatePage() {
  const [fields, setFields] = useState<MonitorFormFields>(DEFAULT_MONITOR_FIELDS);
  const [intervals, setIntervals] = useState<Intervals | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, FieldIssue>>({});
  const [formMessage, setFormMessage] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [issued, setIssued] = useState<{ token: string; id: string; ingestPath: string } | null>(
    null,
  );
  const choice = useRef<IntervalChoice>(intervalChoice());

  useEffect(() => {
    const controller = new AbortController();
    listIntervals(controller.signal)
      .then((loaded) => {
        if (!controller.signal.aborted) setIntervals(loaded);
      })
      .catch(() => {
        // The selector stays empty and the backend default applies on submit.
      });
    return () => controller.abort();
  }, []);

  const submit = () => {
    setPending(true);
    setFieldErrors({});
    setFormMessage(null);
    createMonitor(
      fields.kind === 'heartbeat'
        ? {
            kind: 'heartbeat',
            name: fields.name,
            heartbeat: {
              intervalSeconds: fields.heartbeatIntervalSeconds,
              graceSeconds: fields.heartbeatGraceSeconds,
            },
            incidentPolicy: incidentPolicyFromFields(fields),
          }
        : {
            name: fields.name,
            check: checkInputFromFields(fields),
            intervalSeconds: choice.current.current ?? intervals?.defaultIntervalSeconds,
            incidentPolicy: incidentPolicyFromFields(fields),
          },
    )
      .then((monitor) => {
        setPending(false);
        if (monitor.kind === 'heartbeat' && monitor.issuedToken && monitor.heartbeat) {
          setIssued({
            token: monitor.issuedToken,
            id: monitor.id,
            ingestPath: monitor.heartbeat.ingestPath,
          });
        } else navigate(`/monitors/${encodeURIComponent(monitor.id)}`);
      })
      .catch((error: unknown) => {
        if (isAbortError(error)) return;
        setPending(false);
        if (error instanceof ApiValidationError) {
          const split = splitFieldErrors(error.fields, MONITOR_FIELD_PATHS);
          setFieldErrors(split.fieldErrors);
          setFormMessage(split.formMessage);
          return;
        }
        setFormMessage(describeApiError(error));
      });
  };

  return (
    <section aria-labelledby="create-monitor-heading">
      <h2 id="create-monitor-heading">New monitor</h2>
      <p>
        Choose an HTTP check for a loopback target, or a heartbeat to receive completion reports
        from a job.
      </p>
      {issued === null ? (
        <MonitorForm
          allowKindChoice
          fields={fields}
          onFieldsChange={setFields}
          fieldErrors={fieldErrors}
          formMessage={formMessage}
          pending={pending}
          intervals={intervals}
          onIntervalChange={(seconds) => {
            choice.current.current = seconds;
          }}
          submitLabel="Create monitor"
          onSubmit={submit}
        />
      ) : (
        <>
          <p role="status">Heartbeat created. Token shown once.</p>
          <HeartbeatTokenPanel
            token={issued.token}
            focusOnShow
            ingestPath={issued.ingestPath}
            onDismiss={() => navigate(`/monitors/${encodeURIComponent(issued.id)}`)}
          />
        </>
      )}
      <p>
        <Link to="/monitors">Back to monitors</Link>
      </p>
    </section>
  );
}

export default MonitorCreatePage;
