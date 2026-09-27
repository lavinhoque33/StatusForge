import { useState } from 'react';
import { ApiValidationError, isAbortError, type FieldIssue } from '../api/http';
import { createMonitor } from '../api/monitors';
import { MonitorForm } from '../components/MonitorForm';
import { describeApiError } from '../lib/errors';
import {
  DEFAULT_MONITOR_FIELDS,
  MONITOR_FIELD_PATHS,
  checkInputFromFields,
  splitFieldErrors,
  type MonitorFormFields,
} from '../lib/monitorForm';
import { navigate } from '../router/history';
import { Link } from '../router/Link';

/** Create form for a new monitor. */
export function MonitorCreatePage() {
  const [fields, setFields] = useState<MonitorFormFields>(DEFAULT_MONITOR_FIELDS);
  const [fieldErrors, setFieldErrors] = useState<Record<string, FieldIssue>>({});
  const [formMessage, setFormMessage] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const submit = () => {
    setPending(true);
    setFieldErrors({});
    setFormMessage(null);
    createMonitor({ name: fields.name, check: checkInputFromFields(fields) })
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
        A monitor checks one loopback http target when you ask for a check. Only hosts listed in
        STATUSFORGE_ALLOWED_TARGETS are accepted.
      </p>
      <MonitorForm
        fields={fields}
        onFieldsChange={setFields}
        fieldErrors={fieldErrors}
        formMessage={formMessage}
        pending={pending}
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
