/** Fixtures and a boundary-level `fetch` stub shared by the web tests. */
import { vi, type Mock } from 'vitest';
import type {
  CheckConfig,
  Gap,
  Monitor,
  MonitorRecord,
  MonitorStatus,
  Observation,
} from '../api/monitors';

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
    applicationId: null,
    name: 'Sample target',
    lifecycle: 'active',
    configVersion: 1,
    kind: 'http',
    heartbeat: null,
    expectation: null,
    intervalSeconds: 300,
    check: checkFixture(),
    incidentPolicy: { openAfter: 2, recoverAfter: 2 },
    openIncident: null,
    maintenance: { active: null, next: null },
    deletion: null,
    createdAt: '2026-09-27T10:00:00.000Z',
    updatedAt: '2026-09-27T10:00:00.000Z',
    ...overrides,
  };
}

/** A presented status; defaults to `unknown`/`no_checks`. */
export function monitorStatusFixture(overrides: Partial<MonitorStatus> = {}): MonitorStatus {
  return {
    state: 'unknown',
    reason: 'no_checks',
    observation: null,
    freshUntil: null,
    evaluatedAt: '2026-09-27T12:00:00.000Z',
    ...overrides,
  };
}

/** A monitor record: the monitor plus its presented status. */
export function monitorRecordFixture(
  overrides: Partial<MonitorRecord> = {},
  statusOverrides: Partial<MonitorStatus> = {},
): MonitorRecord {
  return {
    ...monitorFixture(overrides),
    status: monitorStatusFixture(statusOverrides),
  };
}

export function observationFixture(overrides: Partial<Observation> = {}): Observation {
  return {
    kind: 'http_check',
    report: null,
    id: 'observation-1',
    monitorId: 'monitor-1',
    configVersion: 1,
    initiatedBy: 'manual',
    trigger: null,
    dueAt: null,
    counted: false,
    notCountedReason: null,
    maintenanceWindowId: null,
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

/** A missed-slot range. */
export function gapFixture(overrides: Partial<Gap> = {}): Gap {
  return {
    id: 'gap-1',
    monitorId: 'monitor-1',
    fromDueAt: '2026-09-27T10:05:00.000Z',
    toDueAt: '2026-09-27T10:20:00.000Z',
    missedCount: 4,
    reason: 'not_scheduled',
    recordedAt: '2026-09-27T10:25:00.000Z',
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
