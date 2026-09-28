import type { FormEvent } from 'react';
import type { FieldIssue } from '../api/http';
import type { Intervals } from '../api/monitors';
import { IntervalField } from './IntervalField';
import { fieldErrorMessage } from '../lib/errors';
import { intervalLabel } from '../lib/intervals';
import type { MonitorFormFields } from '../lib/monitorForm';

type MonitorFormProps = {
  fields: MonitorFormFields;
  onFieldsChange: (fields: MonitorFormFields) => void;
  allowKindChoice?: boolean;
  fieldErrors: Record<string, FieldIssue>;
  formMessage: string | null;
  pending: boolean;
  disabled?: boolean;
  /** The intervals the backend offers; null while loading (no selector yet). */
  intervals: Intervals | null;
  /** The stored interval, so a no-longer-offered value stays visible. */
  storedIntervalSeconds?: number;
  /** Receives the user's interval choice; the page sends it on submit. */
  onIntervalChange?: (seconds: number) => void;
  submitLabel: string;
  onSubmit: () => void;
};

type TextFieldProps = {
  id: string;
  name: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  error: FieldIssue | undefined;
  hint?: string;
  type?: 'text' | 'number';
  min?: number;
  max?: number;
  step?: number;
  disabled?: boolean;
};

function TextField({
  id,
  name,
  label,
  value,
  onChange,
  error,
  hint,
  type = 'text',
  min,
  max,
  step,
  disabled = false,
}: TextFieldProps) {
  const errorId = `${id}-error`;
  const hintId = hint === undefined ? undefined : `${id}-hint`;
  const describedBy = [hintId, error === undefined ? undefined : errorId]
    .filter((entry) => entry !== undefined)
    .join(' ');

  return (
    <div className="form-field">
      <label htmlFor={id}>{label}</label>
      {hint === undefined ? null : (
        <p className="form-hint" id={hintId}>
          {hint}
        </p>
      )}
      <input
        id={id}
        name={name}
        type={type}
        value={value}
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        aria-invalid={error === undefined ? undefined : true}
        aria-describedby={describedBy === '' ? undefined : describedBy}
        onChange={(event) => onChange(event.target.value)}
      />
      {error === undefined ? null : (
        <p className="field-error" id={errorId}>
          {fieldErrorMessage(error)}
        </p>
      )}
    </div>
  );
}

/**
 * Name, URL, expected status, and deadline fields shared by create and edit.
 *
 * The form is controlled by its page; validation belongs to the backend, so the
 * form has no client-side gate and renders whatever `fields` reported.
 */
export function MonitorForm({
  fields,
  onFieldsChange,
  fieldErrors,
  formMessage,
  pending,
  disabled = false,
  intervals,
  allowKindChoice = false,
  storedIntervalSeconds,
  onIntervalChange,
  submitLabel,
  onSubmit,
}: MonitorFormProps) {
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    onSubmit();
  };

  return (
    <form className="monitor-form" onSubmit={submit} noValidate>
      <TextField
        id="monitor-name"
        name="name"
        label="Name"
        value={fields.name}
        onChange={(name) => onFieldsChange({ ...fields, name })}
        error={fieldErrors['name']}
        disabled={disabled}
      />
      {allowKindChoice ? (
        <div className="form-field">
          <label htmlFor="monitor-kind">Type</label>
          <select
            id="monitor-kind"
            value={fields.kind}
            disabled={disabled}
            onChange={(event) =>
              onFieldsChange({
                ...fields,
                kind: event.target.value as MonitorFormFields['kind'],
                openAfter: event.target.value === 'heartbeat' ? '1' : '2',
                recoverAfter: event.target.value === 'heartbeat' ? '1' : '2',
              })
            }
          >
            <option value="http">HTTP check</option>
            <option value="heartbeat">Heartbeat</option>
          </select>
        </div>
      ) : null}
      {fields.kind === 'http' ? (
        <>
          <TextField
            id="monitor-url"
            name="url"
            label="URL"
            hint="Loopback http URL, for example http://127.0.0.1:8090/healthy"
            value={fields.url}
            onChange={(url) => onFieldsChange({ ...fields, url })}
            error={fieldErrors['check.url']}
            disabled={disabled}
          />
          <TextField
            id="monitor-expected-status"
            name="expectedStatus"
            label="Expected status"
            type="number"
            min={100}
            max={599}
            step={1}
            value={fields.expectedStatus}
            onChange={(expectedStatus) => onFieldsChange({ ...fields, expectedStatus })}
            error={fieldErrors['check.expectedStatus']}
            disabled={disabled}
          />
          <TextField
            id="monitor-deadline-seconds"
            name="deadlineSeconds"
            label="Deadline (seconds)"
            hint="1 to 30 seconds"
            type="number"
            min={1}
            max={30}
            step={1}
            value={fields.deadlineSeconds}
            onChange={(deadlineSeconds) => onFieldsChange({ ...fields, deadlineSeconds })}
            error={fieldErrors['check.deadlineMs']}
            disabled={disabled}
          />
        </>
      ) : (
        <>
          <div className="form-field">
            <label htmlFor="heartbeat-interval">Heartbeat interval</label>
            <select
              id="heartbeat-interval"
              value={fields.heartbeatIntervalSeconds}
              disabled={disabled || intervals === null}
              onChange={(event) => {
                const interval = Number(event.target.value);
                onFieldsChange({
                  ...fields,
                  heartbeatIntervalSeconds: interval,
                  heartbeatGraceSeconds: Math.min(
                    fields.heartbeatGraceSeconds,
                    intervals?.heartbeat.graceSeconds
                      .filter((seconds) => seconds <= interval)
                      .at(-1) ?? interval,
                  ),
                });
              }}
            >
              {(intervals?.heartbeat.intervalSeconds ?? [fields.heartbeatIntervalSeconds]).map(
                (seconds) => (
                  <option key={seconds} value={seconds}>
                    {intervalLabel(seconds)}
                  </option>
                ),
              )}
            </select>
            {fieldErrors['heartbeat.intervalSeconds'] ? (
              <span role="alert">
                {fieldErrorMessage(fieldErrors['heartbeat.intervalSeconds'])}
              </span>
            ) : null}
          </div>
          <div className="form-field">
            <label htmlFor="heartbeat-grace">Grace period</label>
            <select
              id="heartbeat-grace"
              value={fields.heartbeatGraceSeconds}
              disabled={disabled || intervals === null}
              onChange={(event) =>
                onFieldsChange({ ...fields, heartbeatGraceSeconds: Number(event.target.value) })
              }
            >
              {(intervals?.heartbeat.graceSeconds ?? [fields.heartbeatGraceSeconds])
                .filter((seconds) => seconds <= fields.heartbeatIntervalSeconds)
                .map((seconds) => (
                  <option key={seconds} value={seconds}>
                    {intervalLabel(seconds)}
                  </option>
                ))}
            </select>
            {fieldErrors['heartbeat.graceSeconds'] ? (
              <span role="alert">{fieldErrorMessage(fieldErrors['heartbeat.graceSeconds'])}</span>
            ) : null}
          </div>
        </>
      )}
      <TextField
        id="monitor-open-after"
        name="openAfter"
        label="Open after failed checks"
        hint="1 to 5 consecutive counted failed checks"
        type="number"
        min={1}
        max={5}
        step={1}
        value={fields.openAfter}
        onChange={(openAfter) => onFieldsChange({ ...fields, openAfter })}
        error={fieldErrors['incidentPolicy.openAfter']}
        disabled={disabled}
      />
      <TextField
        id="monitor-recover-after"
        name="recoverAfter"
        label="Resolve after healthy checks"
        hint="1 to 5 consecutive counted healthy checks"
        type="number"
        min={1}
        max={5}
        step={1}
        value={fields.recoverAfter}
        onChange={(recoverAfter) => onFieldsChange({ ...fields, recoverAfter })}
        error={fieldErrors['incidentPolicy.recoverAfter']}
        disabled={disabled}
      />
      {fields.kind === 'http' ? (
        <IntervalField
          id="monitor-interval"
          intervals={intervals}
          storedSeconds={storedIntervalSeconds}
          onChange={(seconds) => onIntervalChange?.(seconds)}
          disabled={disabled}
        />
      ) : null}
      {formMessage === null ? null : (
        <p className="form-message" role="alert">
          {formMessage}
        </p>
      )}
      <button type="submit" className="button" disabled={pending || disabled}>
        {pending ? 'Saving…' : submitLabel}
      </button>
    </form>
  );
}

export default MonitorForm;
