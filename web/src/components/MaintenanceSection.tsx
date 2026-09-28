import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ApiRequestError, ApiValidationError, type FieldIssue } from '../api/http';
import { cancelMaintenance, scheduleMaintenance, type Window } from '../api/maintenance';
import { describeApiError, fieldErrorMessage } from '../lib/errors';
import { localDateTimeToUtc, scheduleZoneLabel } from '../lib/maintenanceTime';
import { formatLocalWithOffset } from '../lib/time';

export function MaintenanceSection({
  monitorId,
  archived,
  windows,
  refresh,
}: {
  monitorId: string;
  archived: boolean;
  windows: Window[];
  refresh: () => void;
}) {
  const [fields, setFields] = useState({ startAt: '', endAt: '', note: '' });
  const [errors, setErrors] = useState<Record<string, FieldIssue>>({});
  const [localError, setLocalError] = useState<{
    field: 'startAt' | 'endAt';
    message: string;
  } | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [confirmId, setConfirmId] = useState<string | null>(null);
  const [focusId, setFocusId] = useState<string | null>(null);
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const zone = scheduleZoneLabel();

  useEffect(() => {
    if (focusId === null) return;
    const target =
      focusId === 'maintenance-heading'
        ? document.getElementById('maintenance-heading')
        : buttons.current.get(focusId);
    if (target) {
      target.focus();
      setFocusId(null);
    }
  }, [focusId, confirmId, windows]);

  const schedule = async (event: FormEvent) => {
    event.preventDefault();
    if (pending || archived) return;
    setErrors({});
    setLocalError(null);
    setNotice(null);
    let startAt: string;
    let endAt: string;
    try {
      startAt = localDateTimeToUtc(fields.startAt);
    } catch (error) {
      setLocalError({ field: 'startAt', message: (error as Error).message });
      return;
    }
    try {
      endAt = localDateTimeToUtc(fields.endAt);
    } catch (error) {
      setLocalError({ field: 'endAt', message: (error as Error).message });
      return;
    }
    setPending(true);
    try {
      await scheduleMaintenance(monitorId, { startAt, endAt, note: fields.note });
      setFields({ startAt: '', endAt: '', note: '' });
      setNotice('Maintenance window scheduled.');
      refresh();
    } catch (error) {
      if (error instanceof ApiValidationError) {
        setErrors(error.fields);
        setNotice('Review the highlighted schedule values.');
      } else {
        setNotice(describeApiError(error));
        if (error instanceof ApiRequestError && error.code === 'archived') refresh();
      }
    } finally {
      setPending(false);
    }
  };

  const close = async (window: Window) => {
    if (pending) return;
    setPending(true);
    setNotice(null);
    try {
      await cancelMaintenance(monitorId, window.id);
      setNotice(window.state === 'active' ? 'Maintenance ended.' : 'Maintenance cancelled.');
      setConfirmId(null);
      refresh();
      setFocusId('maintenance-heading');
    } catch (error) {
      if (error instanceof ApiRequestError && error.code === 'window_closed') {
        setNotice('This window has already ended or been cancelled. Windows reloaded.');
        setConfirmId(null);
        refresh();
        setFocusId('maintenance-heading');
      } else setNotice(describeApiError(error));
    } finally {
      setPending(false);
    }
  };

  const ordered = [...windows].sort((a, b) => {
    const currentA = a.state === 'active' || a.state === 'scheduled';
    const currentB = b.state === 'active' || b.state === 'scheduled';
    return currentA === currentB
      ? currentA
        ? a.startAt.localeCompare(b.startAt)
        : b.startAt.localeCompare(a.startAt)
      : currentA
        ? -1
        : 1;
  });
  return (
    <section className="panel" aria-labelledby="maintenance-heading">
      <h3 id="maintenance-heading" tabIndex={-1}>
        Maintenance
      </h3>
      <p>Checks continue during maintenance; labelled results do not open or resolve incidents.</p>
      {archived ? (
        <p>Archived monitors cannot schedule or change windows.</p>
      ) : (
        <form className="monitor-form" onSubmit={(event) => void schedule(event)} noValidate>
          <p className="form-hint">{zone}. Enter local times; windows may last up to 7 days.</p>
          {(['startAt', 'endAt'] as const).map((field) => (
            <div className="form-field" key={field}>
              <label htmlFor={`maintenance-${field}`}>
                {field === 'startAt' ? 'Start' : 'End'}
              </label>
              <input
                id={`maintenance-${field}`}
                type="datetime-local"
                value={fields[field]}
                disabled={pending}
                aria-invalid={errors[field] || localError?.field === field ? true : undefined}
                aria-describedby={
                  errors[field] || localError?.field === field
                    ? `maintenance-${field}-error`
                    : undefined
                }
                onChange={(event) =>
                  setFields((current) => ({ ...current, [field]: event.target.value }))
                }
              />
              {errors[field] || localError?.field === field ? (
                <p className="field-error" id={`maintenance-${field}-error`}>
                  {localError?.field === field
                    ? localError.message
                    : fieldErrorMessage(errors[field])}
                </p>
              ) : null}
            </div>
          ))}
          <div className="form-field">
            <label htmlFor="maintenance-note">Note (optional)</label>
            <textarea
              id="maintenance-note"
              value={fields.note}
              maxLength={200}
              disabled={pending}
              aria-invalid={errors.note ? true : undefined}
              aria-describedby={errors.note ? 'maintenance-note-error' : undefined}
              onChange={(event) =>
                setFields((current) => ({ ...current, note: event.target.value }))
              }
            />
            {errors.note ? (
              <p className="field-error" id="maintenance-note-error">
                {fieldErrorMessage(errors.note)}
              </p>
            ) : null}
          </div>
          {Object.entries(errors)
            .filter(([path]) => !['startAt', 'endAt', 'note'].includes(path))
            .map(([path, issue]) => (
              <p className="field-error" key={path}>
                {fieldErrorMessage(issue)}
              </p>
            ))}
          <button className="button" type="submit" disabled={pending}>
            Schedule maintenance
          </button>
        </form>
      )}
      {notice ? (
        <p role="status" className="notice">
          {notice}
        </p>
      ) : null}
      <h4>Windows</h4>
      {ordered.length === 0 ? (
        <p>No maintenance windows recorded.</p>
      ) : (
        <ul className="maintenance-windows">
          {ordered.map((window) => {
            const actionable =
              !archived && (window.state === 'active' || window.state === 'scheduled');
            const action = window.state === 'active' ? 'End now' : 'Cancel';
            return (
              <li key={window.id} className="maintenance-window">
                <strong>{window.state[0].toUpperCase() + window.state.slice(1)}</strong> —{' '}
                <time dateTime={window.startAt}>
                  {formatLocalWithOffset(new Date(window.startAt))}
                </time>{' '}
                to{' '}
                <time dateTime={window.endAt}>{formatLocalWithOffset(new Date(window.endAt))}</time>
                {window.note ? <p>{window.note}</p> : null}
                {actionable &&
                  (confirmId === window.id ? (
                    <div className="confirm-step">
                      <span>
                        {window.state === 'active'
                          ? 'End this maintenance window now?'
                          : 'Cancel this scheduled maintenance window?'}
                      </span>
                      <button
                        type="button"
                        className="button"
                        disabled={pending}
                        autoFocus
                        onClick={() => void close(window)}
                      >
                        Confirm {action.toLowerCase()}
                      </button>
                      <button
                        type="button"
                        className="button"
                        disabled={pending}
                        onClick={() => {
                          setConfirmId(null);
                          setFocusId(`window-${window.id}`);
                        }}
                      >
                        Keep window
                      </button>
                    </div>
                  ) : (
                    <button
                      type="button"
                      className="button"
                      disabled={pending}
                      ref={(node) => {
                        if (node) buttons.current.set(`window-${window.id}`, node);
                        else buttons.current.delete(`window-${window.id}`);
                      }}
                      onClick={() => setConfirmId(window.id)}
                    >
                      {action}
                    </button>
                  ))}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
