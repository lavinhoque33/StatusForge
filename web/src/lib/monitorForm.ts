/**
 * Form text ↔ contract payload conversion for the create and edit forms.
 *
 * Numbers are converted optimistically and the backend keeps ownership of the
 * ranges: an empty or unparsable value becomes `0`, which the contract answers
 * with `out_of_range` on that field, so the user sees one error source.
 */
import type { FieldIssue } from '../api/http';
import type { CheckInput, Monitor } from '../api/monitors';
import { fieldErrorMessage } from './errors';

export type MonitorFormFields = {
  name: string;
  url: string;
  expectedStatus: string;
  deadlineSeconds: string;
  openAfter: string;
  recoverAfter: string;
};

/** Defaults for a new monitor. */
export const DEFAULT_MONITOR_FIELDS: MonitorFormFields = {
  name: '',
  url: 'http://127.0.0.1:8090/',
  expectedStatus: '200',
  deadlineSeconds: '10',
  openAfter: '2',
  recoverAfter: '2',
};

/** Field paths the monitor form renders as per-input errors. */
export const MONITOR_FIELD_PATHS: readonly string[] = [
  'name',
  'check.url',
  'check.expectedStatus',
  'check.deadlineMs',
  'incidentPolicy.openAfter',
  'incidentPolicy.recoverAfter',
];

/** Fill the form from a stored monitor (deadline shown in whole seconds). */
export function monitorFormFields(monitor: Monitor): MonitorFormFields {
  return {
    name: monitor.name,
    url: monitor.check.url,
    expectedStatus: String(monitor.check.expectedStatus),
    deadlineSeconds: String(monitor.check.deadlineMs / 1000),
    openAfter: String(monitor.incidentPolicy.openAfter),
    recoverAfter: String(monitor.incidentPolicy.recoverAfter),
  };
}

/** Build the `check` payload from the form (deadline entered in seconds). */
export function checkInputFromFields(fields: MonitorFormFields): CheckInput {
  const expectedStatus = Number(fields.expectedStatus.trim());
  const deadlineSeconds = Number(fields.deadlineSeconds.trim());
  return {
    url: fields.url.trim(),
    expectedStatus: Number.isFinite(expectedStatus) ? expectedStatus : 0,
    deadlineMs: Number.isFinite(deadlineSeconds) ? Math.round(deadlineSeconds * 1000) : 0,
  };
}

export function incidentPolicyFromFields(fields: MonitorFormFields) {
  const openAfter = Number(fields.openAfter.trim());
  const recoverAfter = Number(fields.recoverAfter.trim());
  return {
    openAfter: Number.isFinite(openAfter) ? openAfter : 0,
    recoverAfter: Number.isFinite(recoverAfter) ? recoverAfter : 0,
  };
}

/**
 * Split `validation_failed` fields into per-input errors and a form message for
 * paths this form does not render (`expectedConfigVersion`, `action`, ...).
 */
export function splitFieldErrors(
  fields: Record<string, FieldIssue>,
  knownPaths: readonly string[],
): { fieldErrors: Record<string, FieldIssue>; formMessage: string | null } {
  const fieldErrors: Record<string, FieldIssue> = {};
  const formIssues: string[] = [];
  for (const [path, issue] of Object.entries(fields)) {
    if (knownPaths.includes(path)) fieldErrors[path] = issue;
    else formIssues.push(fieldErrorMessage(issue));
  }
  return { fieldErrors, formMessage: formIssues.length === 0 ? null : formIssues.join(' ') };
}
