import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Attempt, Evidence, Incident, Notification } from '../api/incidents';
import {
  attemptResultWords,
  mergeIncidentTimeline,
  resolutionWords,
} from '../lib/incidentPresentation';
import { gapFixture, jsonResponse, stubApi, callsTo } from '../test/fixtures';
import { IncidentDetailPage } from './IncidentDetailPage';

const at = '2026-09-27T10:00:00.000Z';
const evidence: Evidence = {
  observationId: 'o1',
  startedAt: at,
  initiatedBy: 'scheduled',
  outcome: 'failing',
  reason: 'wrong_status',
  observedStatus: 503,
  configVersion: 1,
};
const incident: Incident = {
  id: 'incident-1',
  monitorId: 'monitor-1',
  monitorName: 'Sample',
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
const attempt: Attempt = {
  number: 1,
  startedAt: at,
  completedAt: at,
  manual: false,
  result: 'http_error',
  httpStatus: 503,
  durationMs: 40,
};
const note: Notification = {
  id: 'incident-1:opened',
  kind: 'opened',
  reminderSeq: null,
  state: 'failed',
  createdAt: at,
  nextAttemptAt: null,
  deliveredAt: null,
  failedAt: at,
  cancelledReason: null,
  attempts: [attempt],
};
const path = 'GET /api/monitors/monitor-1/incidents/incident-1';
const retryPath = 'POST /api/monitors/monitor-1/incidents/incident-1/notifications/opened/retry';
const detail = {
  incident,
  events: [{ type: 'opened', at, details: {} }],
  gaps: [],
  notifications: [note],
};

afterEach(() => vi.unstubAllGlobals());
describe('incident presentation', () => {
  it('renders every attempt result in words without treating unknown delivery as success', () => {
    const cases: [Attempt['result'], string][] = [
      ['delivered', 'Delivered'],
      ['http_error', 'Receiver error (HTTP 503)'],
      ['rejected', 'Rejected (HTTP 503)'],
      ['timeout', 'Timed out'],
      ['connection_refused', 'Receiver unreachable'],
      ['outcome_unknown', 'Outcome unknown — may have been received'],
      ['connection_error', 'Connection error'],
      ['refused_by_policy', 'Destination refused by policy'],
      ['process_stopped', 'Interrupted — StatusForge stopped'],
      ['in_flight', 'In flight'],
    ];
    for (const [result, words] of cases)
      expect(attemptResultWords({ ...attempt, result })).toBe(words);
    expect(resolutionWords('recovered')).toBe('Recovered');
    expect(resolutionWords('archived')).toBe('Ended — monitor archived');
  });
  it('merges gaps and events oldest first even when the server arrays are individually out of order', () => {
    const merged = mergeIncidentTimeline(
      [
        { type: 'resolved', at: '2026-09-27T10:04:00.000Z', details: { resolution: 'recovered' } },
        { type: 'opened', at, details: {} },
      ],
      [gapFixture({ fromDueAt: '2026-09-27T10:02:00.000Z' })],
    );
    expect(merged.map((item) => (item.kind === 'event' ? item.event.type : 'gap'))).toEqual([
      'opened',
      'gap',
      'resolved',
    ]);
  });
  it('queues retry once, keeps focus on the pending button, and replaces failed state', async () => {
    let release: ((response: Response) => void) | undefined;
    const fetchMock = stubApi({
      [path]: () => jsonResponse(detail),
      [retryPath]: () =>
        new Promise<Response>((resolve) => {
          release = resolve;
        }),
    });
    render(<IncidentDetailPage monitorId="monitor-1" incidentId="incident-1" />);
    const button = await screen.findByRole('button', { name: 'Retry delivery' });
    button.focus();
    fireEvent.click(button);
    expect(button).toHaveAttribute('aria-disabled', 'true');
    expect(button).toHaveFocus();
    fireEvent.click(button);
    expect(callsTo(fetchMock, 'POST', retryPath.slice(5))).toHaveLength(1);
    release?.(jsonResponse({ ...note, state: 'pending', failedAt: null }, 202));
    expect(
      await screen.findByText('Retry queued. Delivery will be attempted once.'),
    ).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: 'Opened — pending' })).toHaveFocus();
    expect(screen.getByText(/Receiver error/).textContent).toContain(
      'Receiver error (HTTP 503) · 40 ms',
    );
    expect(screen.queryByRole('button', { name: 'Retry delivery' })).not.toBeInTheDocument();
  });
  it('on 409 reloads server state rather than retrying again', async () => {
    let reads = 0;
    const fetchMock = stubApi({
      [path]: () =>
        jsonResponse({
          ...detail,
          notifications: [reads++ === 0 ? note : { ...note, state: 'delivered', failedAt: null }],
        }),
      [retryPath]: () => jsonResponse({ error: 'not_failed' }, 409),
    });
    render(<IncidentDetailPage monitorId="monitor-1" incidentId="incident-1" />);
    fireEvent.click(await screen.findByRole('button', { name: 'Retry delivery' }));
    await waitFor(() =>
      expect(screen.getByText(/Notification changed and is no longer failed/)).toBeInTheDocument(),
    );
    expect(reads).toBe(2);
    expect(callsTo(fetchMock, 'POST', retryPath.slice(5))).toHaveLength(1);
    expect(await screen.findByRole('heading', { name: 'Opened — delivered' })).toHaveFocus();
    expect(screen.queryByRole('button', { name: 'Retry delivery' })).not.toBeInTheDocument();
  });
});
