/**
 * Client for the backend readiness endpoint.
 *
 * The client speaks only to same-origin `/api` paths (the Vite dev server
 * proxies them to the backend), so no endpoint URL or credential ever enters
 * the browser bundle.
 */

const READINESS_PATH = '/api/health/ready';

/** Coarse failure class; never raw error text. */
export type DependencyReason = 'timeout' | 'unreachable' | 'error';

/** A single dependency as reported by the backend readiness endpoint. */
export type DependencyStatus = {
  status: 'ready' | 'unavailable';
  reason?: DependencyReason;
};

/** The validated readiness payload. */
export type Readiness = {
  status: 'ready' | 'degraded';
  checkedAt: string;
  dependencies: Record<string, DependencyStatus>;
};

/** The backend answered with something this client cannot interpret. */
export class HealthInvalidResponseError extends Error {
  constructor() {
    super('The readiness endpoint returned an unexpected response.');
    this.name = 'HealthInvalidResponseError';
  }
}

/** The readiness request could not reach the backend at all. */
export class HealthUnreachableError extends Error {
  constructor() {
    super('The readiness endpoint could not be reached.');
    this.name = 'HealthUnreachableError';
  }
}

/** The caller's deadline elapsed before the backend answered. */
export class HealthTimeoutError extends Error {
  constructor() {
    super('The readiness request timed out.');
    this.name = 'HealthTimeoutError';
  }
}

/** Validate the readiness payload, rejecting unknown statuses and missing fields. */
function parseReadiness(body: unknown): Readiness {
  if (typeof body !== 'object' || body === null) {
    throw new HealthInvalidResponseError();
  }
  const payload = body as { status?: unknown; checkedAt?: unknown; dependencies?: unknown };

  if (payload.status !== 'ready' && payload.status !== 'degraded') {
    throw new HealthInvalidResponseError();
  }
  if (typeof payload.checkedAt !== 'string' || Number.isNaN(Date.parse(payload.checkedAt))) {
    throw new HealthInvalidResponseError();
  }
  if (
    typeof payload.dependencies !== 'object' ||
    payload.dependencies === null ||
    Array.isArray(payload.dependencies)
  ) {
    throw new HealthInvalidResponseError();
  }

  const dependencies: Record<string, DependencyStatus> = {};
  for (const [name, value] of Object.entries(payload.dependencies)) {
    if (name.trim() === '' || typeof value !== 'object' || value === null) {
      throw new HealthInvalidResponseError();
    }
    const dependency = value as { status?: unknown; reason?: unknown };
    if (dependency.status !== 'ready' && dependency.status !== 'unavailable') {
      throw new HealthInvalidResponseError();
    }
    const { reason } = dependency;
    if (reason === undefined || reason === null) {
      dependencies[name] = { status: dependency.status };
      continue;
    }
    if (reason !== 'timeout' && reason !== 'unreachable' && reason !== 'error') {
      throw new HealthInvalidResponseError();
    }
    dependencies[name] = { status: dependency.status, reason };
  }

  return { status: payload.status, checkedAt: payload.checkedAt, dependencies };
}

/**
 * Abort failures are usually `DOMException`s, which are not guaranteed to be
 * `instanceof Error` in every environment, so detect them by name.
 */
function isAbortError(value: unknown): value is Error & { name: 'AbortError' } {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as { name?: unknown }).name === 'AbortError'
  );
}

/**
 * Turn an aborted signal into the failure the caller asked for.
 *
 * A caller cancellation is reported by aborting the signal; a deadline is
 * reported by aborting it with a reason named `TimeoutError` (the shape used by
 * `AbortSignal.timeout()` and by `AbortController.abort(new DOMException(...))`).
 */
function abortFailure(signal: AbortSignal): Error {
  const { reason } = signal;
  if (
    typeof reason === 'object' &&
    reason !== null &&
    (reason as { name?: unknown }).name === 'TimeoutError'
  ) {
    return new HealthTimeoutError();
  }
  if (isAbortError(reason)) {
    return reason;
  }
  if (reason instanceof Error) {
    return reason;
  }
  if (typeof reason === 'string' && reason !== '') {
    return new DOMException(reason, 'AbortError');
  }
  return new DOMException('The request was aborted.', 'AbortError');
}

function requestFailure(error: unknown): Error {
  if (error instanceof HealthInvalidResponseError || isAbortError(error)) {
    return error;
  }
  return new HealthUnreachableError();
}

/**
 * Read `/api/health/ready`.
 *
 * Resolves for HTTP 200 and 503; any other status is an invalid response.
 * Rejects with {@link HealthInvalidResponseError} for unparsable payloads,
 * {@link HealthUnreachableError} when the request never reaches the backend,
 * {@link HealthTimeoutError} when `signal` was aborted past its deadline, and
 * the caller's own `AbortError` for a plain cancellation.
 */
export function fetchReadiness(signal: AbortSignal): Promise<Readiness> {
  return new Promise<Readiness>((resolve, reject) => {
    if (signal.aborted) {
      reject(abortFailure(signal));
      return;
    }

    // Settling on the abort event keeps the timeout deterministic even when a
    // transport ignores the signal.
    const handleAbort = () => {
      reject(abortFailure(signal));
    };
    signal.addEventListener('abort', handleAbort, { once: true });

    const request = async (): Promise<void> => {
      try {
        const response = await fetch(READINESS_PATH, {
          method: 'GET',
          headers: { Accept: 'application/json' },
          cache: 'no-store',
          signal,
        });
        if (response.status !== 200 && response.status !== 503) {
          throw new HealthInvalidResponseError();
        }
        let body: unknown;
        try {
          body = await response.json();
        } catch {
          throw new HealthInvalidResponseError();
        }
        resolve(parseReadiness(body));
      } catch (error) {
        reject(requestFailure(error));
      } finally {
        signal.removeEventListener('abort', handleAbort);
      }
    };

    void request();
  });
}
