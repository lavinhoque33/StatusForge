import type { FormEvent } from 'react';
import type { FieldIssue } from '../api/http';
import type { Intervals } from '../api/monitors';
import { IntervalField } from './IntervalField';
import { fieldErrorMessage } from '../lib/errors';
import type { MonitorFormFields } from '../lib/monitorForm';

type MonitorFormProps = {
  fields: MonitorFormFields;
  onFieldsChange: (fields: MonitorFormFields) => void;
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
      <IntervalField
        id="monitor-interval"
        intervals={intervals}
        storedSeconds={storedIntervalSeconds}
        onChange={(seconds) => onIntervalChange?.(seconds)}
        disabled={disabled}
      />
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
