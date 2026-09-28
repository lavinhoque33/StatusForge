import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiInvalidResponseError } from './http';
import {
  cancelMaintenance,
  listMaintenance,
  parseWindow,
  scheduleMaintenance,
} from './maintenance';
import { callsTo, jsonResponse, stubApi } from '../test/fixtures';

const window = {
  id: 'w1',
  monitorId: 'monitor-1',
  startAt: '2026-09-27T10:00:00.000Z',
  endAt: '2026-09-27T11:00:00.000Z',
  note: '',
  createdAt: '2026-09-27T09:00:00.000Z',
  cancelledAt: null,
  state: 'scheduled',
};
afterEach(() => vi.unstubAllGlobals());

describe('maintenance API', () => {
  it('rejects unknown window state instead of guessing active or scheduled', () => {
    expect(() => parseWindow({ ...window, state: 'paused' })).toThrow(ApiInvalidResponseError);
  });
  it('uses the list, create and close contract routes and UTC payloads', async () => {
    const fetchMock = stubApi({
      'GET /api/monitors/monitor-1/maintenance': () => jsonResponse({ windows: [window] }),
      'POST /api/monitors/monitor-1/maintenance': () => jsonResponse(window, 201),
      'POST /api/monitors/monitor-1/maintenance/w1/cancel': () =>
        jsonResponse({ ...window, state: 'cancelled', cancelledAt: '2026-09-27T10:30:00.000Z' }),
    });
    expect((await listMaintenance('monitor-1'))[0].state).toBe('scheduled');
    const payload = {
      startAt: '2026-09-27T10:00:00.000Z',
      endAt: '2026-09-27T11:00:00.000Z',
      note: '',
    };
    await scheduleMaintenance('monitor-1', payload);
    expect(
      JSON.parse(
        String(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/maintenance')[0][1]?.body),
      ),
    ).toEqual(payload);
    expect((await cancelMaintenance('monitor-1', 'w1')).state).toBe('cancelled');
  });
});
