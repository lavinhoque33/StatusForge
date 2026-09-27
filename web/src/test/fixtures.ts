/** Fixtures and a boundary-level `fetch` stub shared by the web tests. */
import { vi, type Mock } from 'vitest';
import type { CheckConfig, Monitor, Observation } from '../api/monitors';

export function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

export function checkFixture(overrides: Partial<CheckConfig> = {}): CheckConfig {
  return {
    url: 'http://127.0.0.1:8090/',
    method: 'GET',
    expectedStatus: 200,
    deadlineMs: 10000,
    maxBodyBytes: 65536,
    ...overrides,
  };
}

export function monitorFixture(overrides: Partial<Monitor> = {}): Monitor {
  return {
    id: 'monitor-1',
    name: 'Sample target',
    lifecycle: 'active',
    configVersion: 1,
    check: checkFixture(),
    createdAt: '2026-09-27T10:00:00.000Z',
    updatedAt: '2026-09-27T10:00:00.000Z',
    ...overrides,
  };
}

export function observationFixture(overrides: Partial<Observation> = {}): Observation {
  return {
    id: 'observation-1',
    monitorId: 'monitor-1',
    configVersion: 1,
    initiatedBy: 'manual',
    request: checkFixture(),
    startedAt: '2026-09-27T10:15:30.000Z',
    completedAt: '2026-09-27T10:15:30.012Z',
    durationMs: 12,
    outcome: 'healthy',
    reason: 'ok',
    observedStatus: 200,
    bodyBytesRead: 0,
    bodyTruncated: false,
    ...overrides,
  };
}

type Handler = (init: RequestInit) => Response | Promise<Response>;

function requestKey(input: RequestInfo | URL, init?: RequestInit): string {
  const url = typeof input === 'string' ? input : input instanceof URL ? input.pathname : input.url;
  return `${(init?.method ?? 'GET').toUpperCase()} ${url}`;
}

/**
 * Stub `fetch` with one handler per `METHOD /path` key.
 *
 * Requests outside the map answer `599 unexpected_request` so a test that
 * forgot a route fails visibly instead of hanging.
 */
export function stubApi(handlers: Record<string, Handler>): Mock<typeof fetch> {
  const fetchMock = vi.fn<typeof fetch>((input, init) =>
    Promise.resolve(
      handlers[requestKey(input, init ?? undefined)]?.(init ?? {}) ??
        jsonResponse({ error: 'unexpected_request' }, 599),
    ),
  );
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

/** Fetch calls made with the given method and path, in order. */
export function callsTo(
  fetchMock: Mock<typeof fetch>,
  method: string,
  path: string,
): Parameters<typeof fetch>[] {
  return fetchMock.mock.calls.filter(
    ([input, init]) => requestKey(input, init ?? undefined) === `${method} ${path}`,
  );
}
