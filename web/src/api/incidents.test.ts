import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiInvalidResponseError } from './http';
import { getIncident, listAttention, listIncidents } from './incidents';
import { jsonResponse, stubApi } from '../test/fixtures';

const at = '2026-09-27T10:00:00.000Z';
const evidence = {
  observationId: 'o1',
  startedAt: at,
  initiatedBy: 'scheduled',
  outcome: 'failing',
  reason: 'wrong_status',
  observedStatus: 503,
  configVersion: 1,
};
const incident = {
  id: 'i',
  monitorId: 'm',
  monitorName: 'Monitor',
  state: 'open',
  resolution: null,
  openedAt: at,
  resolvedAt: null,
  openingEvidence: [evidence],
  recoveryEvidence: [],
  failureCount: 1,
  firstFailureAt: at,
  lastFailure: evidence,
  checkerProblemCount: 0,
  lastCheckerProblem: null,
  maintenanceObservationCount: 0,
  monitoringPaused: false,
  inMaintenance: false,
  notificationSummary: { delivered: 0, pending: 0, failed: 1 },
};
const note = {
  id: 'i:opened',
  kind: 'opened',
  reminderSeq: null,
  state: 'failed',
  createdAt: at,
  nextAttemptAt: null,
  deliveredAt: null,
  failedAt: at,
  cancelledReason: null,
  attempts: [
    {
      number: 1,
      startedAt: at,
      completedAt: at,
      manual: false,
      result: 'rejected',
      httpStatus: 400,
      durationMs: 2,
    },
  ],
};
afterEach(() => vi.unstubAllGlobals());
describe('incident API boundary', () => {
  it.each([
    { field: 'state', value: 'acknowledged' },
    { field: 'resolution', value: 'ignored' },
  ])('rejects unknown incident $field', async ({ field, value }) => {
    stubApi({
      'GET /api/incidents?state=all&limit=50': () =>
        jsonResponse({ incidents: [{ ...incident, [field]: value }] }),
    });
    await expect(listIncidents()).rejects.toBeInstanceOf(ApiInvalidResponseError);
  });
  it.each([
    { note: { ...note, kind: 'escalated' }, events: [] },
    { note: { ...note, state: 'acknowledged' }, events: [] },
    { note: { ...note, attempts: [{ ...note.attempts[0], result: 'unknown' }] }, events: [] },
    { note, events: [{ type: 'silenced', at, details: {} }] },
  ])('rejects unexpected notification or event vocabulary', async ({ note: candidate, events }) => {
    stubApi({
      'GET /api/monitors/m/incidents/i': () =>
        jsonResponse({ incident, events, gaps: [], notifications: [candidate] }),
    });
    await expect(getIncident('m', 'i')).rejects.toBeInstanceOf(ApiInvalidResponseError);
  });
  it('parses attention identities so links target the correct monitor and incident', async () => {
    stubApi({
      'GET /api/notifications/attention?limit=50': () =>
        jsonResponse({
          notifications: [{ ...note, monitorId: 'm', monitorName: 'Monitor', incidentId: 'i' }],
        }),
    });
    await expect(listAttention()).resolves.toMatchObject([
      { monitorId: 'm', incidentId: 'i', state: 'failed' },
    ]);
  });
});
