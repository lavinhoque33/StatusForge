import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  HealthInvalidResponseError,
  HealthTimeoutError,
  HealthUnreachableError,
  fetchReadiness,
} from './health';

const READY_BODY = {
  status: 'ready',
  checkedAt: '2026-09-27T12:00:00.000Z',
  dependencies: { dynamodb: { status: 'ready' } },
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('fetchReadiness', () => {
  it('parses a ready response and asks for an uncached same-origin document', async () => {
    const fetchMock = vi.fn(async () => jsonResponse(READY_BODY));
    vi.stubGlobal('fetch', fetchMock);
    const signal = new AbortController().signal;

    await expect(fetchReadiness(signal)).resolves.toEqual(READY_BODY);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith('/api/health/ready', {
      method: 'GET',
      headers: { Accept: 'application/json' },
      cache: 'no-store',
      signal,
    });
  });

  it('parses a degraded response with a dependency reason', async () => {
    const body = {
      status: 'degraded',
      checkedAt: '2026-09-27T12:00:00.000Z',
      dependencies: { dynamodb: { status: 'unavailable', reason: 'timeout' } },
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(body, 503)),
    );

    await expect(fetchReadiness(new AbortController().signal)).resolves.toEqual(body);
  });

  it('rejects an unexpected HTTP status as an invalid response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('boom', { status: 500 })),
    );

    await expect(fetchReadiness(new AbortController().signal)).rejects.toBeInstanceOf(
      HealthInvalidResponseError,
    );
  });

  it.each([
    ['a body that is not JSON', new Response('<html>nope</html>', { status: 200 })],
    [
      'an unknown readiness status',
      jsonResponse({ status: 'maybe', checkedAt: '2026-09-27T12:00:00.000Z', dependencies: {} }),
    ],
    ['a missing checkedAt', jsonResponse({ status: 'ready', dependencies: {} })],
    [
      'an unparsable checkedAt',
      jsonResponse({ status: 'ready', checkedAt: 'yesterday', dependencies: {} }),
    ],
    [
      'an unknown dependency status',
      jsonResponse({ ...READY_BODY, dependencies: { dynamodb: { status: 'slow' } } }),
    ],
    [
      'an unknown dependency reason',
      jsonResponse({
        ...READY_BODY,
        dependencies: { dynamodb: { status: 'unavailable', reason: 'exploded' } },
      }),
    ],
    ['a missing dependencies field', jsonResponse({ status: 'ready', checkedAt: '2026-09-27' })],
  ])('rejects %s as an invalid response', async (_description, response) => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => response),
    );

    await expect(fetchReadiness(new AbortController().signal)).rejects.toBeInstanceOf(
      HealthInvalidResponseError,
    );
  });

  it('rejects a transport failure as unreachable', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch');
      }),
    );

    await expect(fetchReadiness(new AbortController().signal)).rejects.toBeInstanceOf(
      HealthUnreachableError,
    );
  });

  it('reports a deadline abort as a timeout, even when the transport stalls', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>(() => {})),
    );
    const controller = new AbortController();

    const result = fetchReadiness(controller.signal);
    controller.abort(new DOMException('deadline passed', 'TimeoutError'));

    await expect(result).rejects.toBeInstanceOf(HealthTimeoutError);
  });

  it('stops waiting once the caller cancels in flight', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>(() => {})),
    );
    const controller = new AbortController();

    const result = fetchReadiness(controller.signal);
    controller.abort();

    const error: unknown = await result.then(
      () => null,
      (reason: unknown) => reason,
    );
    expect(error).not.toBeInstanceOf(HealthTimeoutError);
    expect((error as Error).name).toBe('AbortError');
  });

  it('propagates an AbortError raised by the transport', async () => {
    const abortError = new DOMException('cancelled by caller', 'AbortError');
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(abortError)),
    );

    await expect(fetchReadiness(new AbortController().signal)).rejects.toBe(abortError);
  });
});
