import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  checkFixture,
  gapFixture,
  jsonResponse,
  monitorFixture,
  monitorRecordFixture,
  monitorStatusFixture,
  observationFixture,
  stubApi,
} from '../test/fixtures';
import {
  ApiInvalidResponseError,
  ApiRequestError,
  ApiUnreachableError,
  ApiValidationError,
} from './http';
import {
  changeLifecycle,
  createMonitor,
  getMonitor,
  listGaps,
  listIntervals,
  listMonitors,
  listObservations,
  runCheck,
  updateMonitor,
} from './monitors';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('listMonitors', () => {
  it('reads monitors with and without a presented status', async () => {
    stubApi({
      'GET /api/monitors': () =>
        jsonResponse({
          monitors: [
            monitorRecordFixture(
              { id: 'monitor-1' },
              monitorStatusFixture({ state: 'unknown', reason: 'no_checks' }),
            ),
            monitorRecordFixture(
              { id: 'monitor-2', lifecycle: 'paused' },
              monitorStatusFixture({
                state: 'paused',
                observation: observationFixture({ monitorId: 'monitor-2' }),
              }),
            ),
          ],
        }),
    });

    const monitors = await listMonitors();

    expect(monitors).toHaveLength(2);
    expect(monitors[0]?.status.state).toBe('unknown');
    expect(monitors[1]?.lifecycle).toBe('paused');
    expect(monitors[1]?.status.observation?.outcome).toBe('healthy');
  });

  it('rejects a payload whose monitor is missing required fields', async () => {
    stubApi({ 'GET /api/monitors': () => jsonResponse({ monitors: [{ id: 'monitor-1' }] }) });

    await expect(listMonitors()).rejects.toBeInstanceOf(ApiInvalidResponseError);
  });
});

describe('mutations and reads', () => {
  it('creates a monitor with the contract payload, including the interval', async () => {
    const fetchMock = stubApi({
      'POST /api/monitors': () => jsonResponse(monitorRecordFixture({ id: 'monitor-9' }), 201),
    });

    const monitor = await createMonitor({
      name: 'Local health',
      check: { url: 'http://127.0.0.1:8090/healthy', expectedStatus: 204, deadlineMs: 1500 },
      intervalSeconds: 60,
    });

    expect(monitor.id).toBe('monitor-9');
    const [call] = fetchMock.mock.calls;
    expect(call?.[0]).toBe('/api/monitors');
    expect(call?.[1]?.method).toBe('POST');
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({
      name: 'Local health',
      check: { url: 'http://127.0.0.1:8090/healthy', expectedStatus: 204, deadlineMs: 1500 },
      intervalSeconds: 60,
    });
  });

  it('reads one monitor from its encoded path', async () => {
    const fetchMock = stubApi({
      'GET /api/monitors/monitor%2F1': () =>
        jsonResponse(monitorRecordFixture({ id: 'monitor/1' })),
    });

    await expect(getMonitor('monitor/1')).resolves.toMatchObject({ id: 'monitor/1' });
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/monitors/monitor%2F1');
  });

  it('updates a monitor with the expected configuration version and interval', async () => {
    const fetchMock = stubApi({
      'PATCH /api/monitors/monitor-1': () =>
        jsonResponse(monitorRecordFixture({ configVersion: 2, intervalSeconds: 60 })),
    });

    const monitor = await updateMonitor('monitor-1', {
      expectedConfigVersion: 1,
      name: 'Renamed',
      check: { url: 'http://127.0.0.1:8090/healthy', expectedStatus: 200, deadlineMs: 10000 },
      intervalSeconds: 60,
    });

    expect(monitor.intervalSeconds).toBe(60);
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      expectedConfigVersion: 1,
      name: 'Renamed',
      check: { url: 'http://127.0.0.1:8090/healthy', expectedStatus: 200, deadlineMs: 10000 },
      intervalSeconds: 60,
    });
  });

  it('sends a lifecycle action', async () => {
    const fetchMock = stubApi({
      'POST /api/monitors/monitor-1/lifecycle': () =>
        jsonResponse(
          monitorRecordFixture({ lifecycle: 'paused', pausedAt: '2026-09-27T11:00:00.000Z' }),
        ),
    });

    const monitor = await changeLifecycle('monitor-1', 'pause');

    expect(monitor.lifecycle).toBe('paused');
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ action: 'pause' });
  });

  it('runs a check without a request body', async () => {
    const fetchMock = stubApi({
      'POST /api/monitors/monitor-1/checks': () => jsonResponse(observationFixture(), 201),
    });

    const observation = await runCheck('monitor-1');

    expect(observation.outcome).toBe('healthy');
    expect(fetchMock.mock.calls[0]?.[1]?.body).toBeUndefined();
  });

  it('asks for a bounded observation window', async () => {
    const fetchMock = stubApi({
      'GET /api/monitors/monitor-1/observations?limit=50': () =>
        jsonResponse({ observations: [observationFixture()] }),
    });

    const observations = await listObservations('monitor-1');

    expect(observations).toHaveLength(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/monitors/monitor-1/observations?limit=50');
  });

  it('reads gaps with a bounded window', async () => {
    const fetchMock = stubApi({
      'GET /api/monitors/monitor-1/gaps?limit=50': () => jsonResponse({ gaps: [gapFixture()] }),
    });

    const gaps = await listGaps('monitor-1');

    expect(gaps).toHaveLength(1);
    expect(gaps[0]?.missedCount).toBe(4);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/monitors/monitor-1/gaps?limit=50');
  });

  it('reads the intervals the process offers', async () => {
    stubApi({
      'GET /api/intervals': () =>
        jsonResponse({
          intervalSeconds: [60, 300, 600, 900],
          defaultIntervalSeconds: 300,
          heartbeat: {
            intervalSeconds: [300, 900, 3600],
            graceSeconds: [60, 300, 900],
            defaultIntervalSeconds: 3600,
            defaultGraceSeconds: 900,
          },
        }),
    });

    await expect(listIntervals()).resolves.toEqual({
      intervalSeconds: [60, 300, 600, 900],
      defaultIntervalSeconds: 300,
      heartbeat: {
        intervalSeconds: [300, 900, 3600],
        graceSeconds: [60, 300, 900],
        defaultIntervalSeconds: 3600,
        defaultGraceSeconds: 900,
      },
    });
  });
});

describe('strict parsing', () => {
  it('never accepts an unknown outcome as an observation', async () => {
    stubApi({
      'POST /api/monitors/monitor-1/checks': () =>
        jsonResponse({ ...observationFixture(), outcome: 'unknown' }, 201),
    });

    await expect(runCheck('monitor-1')).rejects.toBeInstanceOf(ApiInvalidResponseError);
  });

  it('never accepts an unknown status state as a presented status', async () => {
    stubApi({
      'GET /api/monitors/monitor-1': () =>
        jsonResponse(
          monitorRecordFixture(undefined, monitorStatusFixture({ state: 'mostly_fine' as never })),
        ),
    });

    await expect(getMonitor('monitor-1')).rejects.toBeInstanceOf(ApiInvalidResponseError);
  });

  it('never accepts an unknown unknown-reason as a presented status', async () => {
    stubApi({
      'GET /api/monitors/monitor-1': () =>
        jsonResponse(
          monitorRecordFixture(undefined, monitorStatusFixture({ reason: 'hedging' as never })),
        ),
    });

    await expect(getMonitor('monitor-1')).rejects.toBeInstanceOf(ApiInvalidResponseError);
  });

  it('reports a monitor response without a status object as unexpected', async () => {
    stubApi({
      'GET /api/monitors/monitor-1': () => jsonResponse(monitorFixture()),
    });

    await expect(getMonitor('monitor-1')).rejects.toBeInstanceOf(ApiInvalidResponseError);
  });

  it('reports a monitor response carrying lastObservation as not-read', async () => {
    stubApi({
      'GET /api/monitors/monitor-1': () =>
        jsonResponse({ ...monitorRecordFixture(), lastObservation: observationFixture() }),
    });

    // The field is no longer part of the API; the client never reads it back.
    const monitor = await getMonitor('monitor-1');
    expect(monitor).toMatchObject({ id: 'monitor-1' });
    expect(monitor).not.toHaveProperty('lastObservation');
  });

  it('reports an unreadable success payload as an unexpected response', async () => {
    stubApi({
      'GET /api/monitors/monitor-1': () => new Response('<html>proxy</html>', { status: 200 }),
    });

    await expect(getMonitor('monitor-1')).rejects.toBeInstanceOf(ApiInvalidResponseError);
  });

  it('reports a transport failure as unreachable', async () => {
    const fetchMock = vi.fn<typeof fetch>(() => Promise.reject(new TypeError('Failed to fetch')));
    vi.stubGlobal('fetch', fetchMock);

    await expect(getMonitor('monitor-1')).rejects.toBeInstanceOf(ApiUnreachableError);
  });
});

describe('error mapping', () => {
  it('maps validation_failed to per-field issues', async () => {
    stubApi({
      'POST /api/monitors': () =>
        jsonResponse(
          {
            error: 'validation_failed',
            fields: {
              'check.url': { code: 'target_not_allowed', message: 'host:port is not allowed' },
            },
          },
          400,
        ),
    });

    const error = await createMonitor({
      name: 'x',
      check: { url: 'http://127.0.0.1:8080/', expectedStatus: 200, deadlineMs: 10000 },
    }).catch((caught: unknown) => caught);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fields['check.url']).toEqual({
      code: 'target_not_allowed',
      message: 'host:port is not allowed',
    });
  });

  it('reports a version conflict once, without retrying the mutation', async () => {
    const fetchMock = stubApi({
      'PATCH /api/monitors/monitor-1': () => jsonResponse({ error: 'version_conflict' }, 409),
    });

    const error = await updateMonitor('monitor-1', {
      expectedConfigVersion: 1,
      name: 'Renamed',
      check: checkFixture(),
    }).catch((caught: unknown) => caught);

    expect(error).toBeInstanceOf(ApiRequestError);
    expect((error as ApiRequestError).status).toBe(409);
    expect((error as ApiRequestError).code).toBe('version_conflict');
    expect(fetchMock.mock.calls).toHaveLength(1);
  });

  it('reports the store as unavailable on 503', async () => {
    stubApi({
      'POST /api/monitors/monitor-1/checks': () =>
        jsonResponse({ error: 'store_unavailable' }, 503),
    });

    const error = await runCheck('monitor-1').catch((caught: unknown) => caught);

    expect((error as ApiRequestError).code).toBe('store_unavailable');
  });

  it('reports a missing monitor with its contract code', async () => {
    stubApi({
      'GET /api/monitors/monitor-1': () => jsonResponse({ error: 'monitor_not_found' }, 404),
    });

    const error = await getMonitor('monitor-1').catch((caught: unknown) => caught);

    expect(error).toBeInstanceOf(ApiRequestError);
    expect((error as ApiRequestError).status).toBe(404);
    expect((error as ApiRequestError).code).toBe('monitor_not_found');
  });

  it('reports an invalid interval selection with its field code', async () => {
    stubApi({
      'PATCH /api/monitors/monitor-1': () =>
        jsonResponse(
          {
            error: 'validation_failed',
            fields: {
              intervalSeconds: {
                code: 'invalid_value',
                message: 'intervalSeconds must be one of 60, 300, 600, 900',
              },
            },
          },
          400,
        ),
    });

    const error = await updateMonitor('monitor-1', {
      expectedConfigVersion: 1,
      name: 'Renamed',
      check: checkFixture(),
      intervalSeconds: 45,
    }).catch((caught: unknown) => caught);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fields['intervalSeconds']?.code).toBe('invalid_value');
  });
});
