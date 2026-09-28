import { useEffect, useRef, useState } from 'react';
import { ApiValidationError, isAbortError, type FieldIssue } from '../api/http';
import { createMonitor, listIntervals, type Intervals } from '../api/monitors';
import { MonitorForm } from '../components/MonitorForm';
import { describeApiError } from '../lib/errors';
import { intervalChoice, type IntervalChoice } from '../lib/intervalChoice';
import {
  DEFAULT_MONITOR_FIELDS,
  MONITOR_FIELD_PATHS,
  checkInputFromFields,
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
    createMonitor({
      name: fields.name,
      check: checkInputFromFields(fields),
      intervalSeconds: choice.current.current ?? intervals?.defaultIntervalSeconds,
    })
      .then((monitor) => {
        setPending(false);
        navigate(`/monitors/${encodeURIComponent(monitor.id)}`);
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
        A monitor checks one loopback http target on a schedule you choose. Only hosts listed in
        STATUSFORGE_ALLOWED_TARGETS are accepted.
      </p>
      <MonitorForm
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
      <p>
        <Link to="/">Back to monitors</Link>
      </p>
    </section>
  );
}

export default MonitorCreatePage;
