/**
 * Shared plumbing for the same-origin `/api` JSON client.
 *
 * One call is one `fetch`: this client never retries, so a mutation that failed
 * (including a `409` conflict) reaches the caller exactly once and the caller
 * decides how to recover. Requests go to relative paths only, so the Vite dev
 * proxy (or the backend itself in a production bundle) serves them.
 */

/** Error codes the M1 monitor contract defines for `{"error":"<code>"}`. */
export type ApiErrorCode =
  | 'not_found'
  | 'method_not_allowed'
  | 'monitor_not_found'
  | 'validation_failed'
  | 'invalid_json'
  | 'body_too_large'
  | 'version_conflict'
  | 'archived'
  | 'invalid_transition'
  | 'check_in_progress'
  | 'store_unavailable';

/** One field problem inside a `validation_failed` response. */
export type FieldIssue = {
  /** Contract code (`required`, `url_invalid`, `target_not_allowed`, ...). */
  code: string;
  /** Human message from the backend; empty when the backend omits it. */
  message: string;
};

/** The backend answered a request with an error body. */
export class ApiRequestError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string) {
    super(`The backend rejected the request (${status} ${code}).`);
    this.name = 'ApiRequestError';
    this.status = status;
    this.code = code;
  }
}

/** `400 validation_failed` with one entry per offending JSON path. */
export class ApiValidationError extends ApiRequestError {
  readonly fields: Record<string, FieldIssue>;

  constructor(status: number, fields: Record<string, FieldIssue>) {
    super(status, 'validation_failed');
    this.name = 'ApiValidationError';
    this.fields = fields;
  }
}

/** The request never reached the backend. */
export class ApiUnreachableError extends Error {
  constructor() {
    super('The backend could not be reached.');
    this.name = 'ApiUnreachableError';
  }
}

/** The backend answered with something this client cannot interpret. */
export class ApiInvalidResponseError extends Error {
  readonly status: number | null;

  constructor(status: number | null = null) {
    super('The backend returned an unexpected response.');
    this.name = 'ApiInvalidResponseError';
    this.status = status;
  }
}

/** Abort failures are `DOMException`s and are not always `instanceof Error`. */
export function isAbortError(value: unknown): value is Error & { name: 'AbortError' } {
  return (
    typeof value === 'object' && value !== null && 'name' in value && value.name === 'AbortError'
  );
}

function parseFieldIssues(value: unknown): Record<string, FieldIssue> {
  if (typeof value !== 'object' || value === null) return {};
  const issues: Record<string, FieldIssue> = {};
  for (const [path, issue] of Object.entries(value)) {
    if (typeof issue !== 'object' || issue === null) continue;
    if (!('code' in issue) || typeof issue.code !== 'string') continue;
    const message = 'message' in issue && typeof issue.message === 'string' ? issue.message : '';
    issues[path] = { code: issue.code, message };
  }
  return issues;
}

function errorFromResponse(
  status: number,
  body: unknown,
): ApiRequestError | ApiInvalidResponseError {
  if (typeof body !== 'object' || body === null) return new ApiInvalidResponseError(status);
  if (!('error' in body)) return new ApiInvalidResponseError(status);
  const { error } = body;
  if (typeof error !== 'string') return new ApiInvalidResponseError(status);
  if (error === 'validation_failed') {
    const fields = 'fields' in body ? parseFieldIssues(body.fields) : {};
    return new ApiValidationError(status, fields);
  }
  return new ApiRequestError(status, error);
}

async function readJson(response: Response): Promise<unknown | undefined> {
  try {
    return await response.json();
  } catch {
    return undefined;
  }
}

type JsonRequestInit = {
  method?: 'GET' | 'POST' | 'PATCH';
  body?: unknown;
  signal?: AbortSignal;
};

/**
 * Perform one JSON request and hand the parsed payload to `parse`.
 *
 * Rejects with {@link ApiRequestError} (or {@link ApiValidationError}) for error
 * bodies, {@link ApiUnreachableError} when the request never arrived,
 * {@link ApiInvalidResponseError} for unparsable payloads, and the caller's own
 * `AbortError` when `signal` was aborted.
 */
export async function requestJson<T>(
  path: string,
  parse: (body: unknown) => T,
  init: JsonRequestInit = {},
): Promise<T> {
  const { method = 'GET', body, signal } = init;
  const hasBody = body !== undefined;

  let response: Response;
  try {
    response = await fetch(path, {
      method,
      headers: hasBody
        ? { Accept: 'application/json', 'Content-Type': 'application/json' }
        : { Accept: 'application/json' },
      body: hasBody ? JSON.stringify(body) : undefined,
      cache: 'no-store',
      signal,
    });
  } catch (error) {
    if (signal?.aborted === true || isAbortError(error)) throw error;
    throw new ApiUnreachableError();
  }

  const payload = await readJson(response);
  if (!response.ok) {
    throw payload === undefined
      ? new ApiInvalidResponseError(response.status)
      : errorFromResponse(response.status, payload);
  }
  if (payload === undefined) {
    throw new ApiInvalidResponseError(response.status);
  }
  return parse(payload);
}
