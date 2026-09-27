/**
 * Copy for API failures. Codes are the contract; the wording here is stable
 * enough to assert in tests and calm enough to show next to a form.
 */
import {
  ApiInvalidResponseError,
  ApiRequestError,
  ApiUnreachableError,
  ApiValidationError,
  type FieldIssue,
} from '../api/http';

const FIELD_MESSAGES: Record<string, string> = {
  required: 'This value is required.',
  too_long: 'This value is too long.',
  too_short: 'This value is too short.',
  out_of_range: 'This value is out of range.',
  invalid_value: 'This value is not accepted.',
  unknown_field: 'This field is not allowed.',
  url_invalid: 'Enter an absolute URL.',
  scheme_not_allowed: 'Only http URLs are allowed in M1.',
  url_has_userinfo: 'Remove the user name and password from the URL.',
  url_has_fragment: 'Remove the #fragment from the URL.',
  host_not_loopback: 'The host must be loopback (127.0.0.0/8, ::1, or localhost).',
  target_not_allowed: 'This host:port is not in STATUSFORGE_ALLOWED_TARGETS.',
};

/** Actionable copy for one field problem; falls back to the contract code. */
export function fieldErrorMessage(issue: FieldIssue): string {
  const message = issue.message.trim();
  if (message !== '') return message;
  return FIELD_MESSAGES[issue.code] ?? 'This value is not accepted.';
}

/** Calm, code-driven copy for failures that have no per-field detail. */
export function describeApiError(error: unknown): string {
  if (error instanceof ApiValidationError) return 'Some values need attention.';
  if (error instanceof ApiRequestError) {
    switch (error.code) {
      case 'store_unavailable':
        return 'The backend store is unavailable. Try again.';
      case 'monitor_not_found':
        return 'This monitor no longer exists.';
      case 'version_conflict':
        return 'This monitor changed; review and try again.';
      case 'archived':
        return 'This monitor is archived; it cannot be changed.';
      case 'check_in_progress':
        return 'A check is already running.';
      case 'invalid_transition':
        return 'That change is not allowed from the current lifecycle state.';
      case 'invalid_json':
        return 'The backend could not read the request.';
      case 'body_too_large':
        return 'The request was too large for the backend.';
      case 'not_found':
      case 'method_not_allowed':
        return 'The backend does not support that operation.';
      default:
        return `The backend rejected the request (${error.code}).`;
    }
  }
  if (error instanceof ApiUnreachableError) return 'The backend could not be reached.';
  if (error instanceof ApiInvalidResponseError) {
    return 'The backend returned an unexpected response.';
  }
  return 'The request failed.';
}
