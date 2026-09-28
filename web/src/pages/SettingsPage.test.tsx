import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { jsonResponse, stubApi } from '../test/fixtures';
import { SettingsPage } from './SettingsPage';

const system = {
  version: 'v0.6.0',
  table: 'statusforge_local',
  dataFormat: 6,
  dataFormatWrittenBy: 'v0.6.0',
  upgradedFrom: 'unmarked',
  backfill: {
    state: 'running',
    stamped: 42,
    startedAt: '2026-09-28T10:00:00.000Z',
    finishedAt: null,
  },
  ttl: { status: 'ENABLING', attribute: 'expiresAt' },
  retention: [
    {
      record: 'observations',
      label: 'Checks',
      days: 90,
      startsFrom: 'Check start',
      protectedWhile: 'Never',
    },
    {
      record: 'incidents',
      label: 'Incidents',
      days: 365,
      startsFrom: 'Resolution',
      protectedWhile: 'Open or pending notifications',
    },
  ],
  housekeeping: {
    intervalSeconds: 60,
    lastRunAt: null,
    pendingRetentionJobs: 2,
    pendingDeletions: 1,
  },
  limits: {
    workers: 4,
    minIntervalSeconds: 60,
    deliveryWorkers: 2,
    deliveryRetrySchedule: ['10s', '30s'],
    reminderIntervalSeconds: 21600,
    livenessIntervalSeconds: 10,
    allowedTargets: '127.0.0.1:8090',
    notifyUrl: 'http://127.0.0.1:8092/accept',
    historyScanBound: 1000,
    summaryObservationLimit: 2000,
    summaryGapLimit: 500,
    summaryWindows: [86400, 604800],
  },
  demo: false,
};
afterEach(() => vi.unstubAllGlobals());
it('shows server retention policy, TTL transition, and housekeeping progress', async () => {
  stubApi({ 'GET /api/system': () => jsonResponse(system) });
  render(<SettingsPage />);
  expect(screen.getByText('Loading settings…')).toBeInTheDocument();
  expect(await screen.findByRole('heading', { name: 'Retention' })).toBeInTheDocument();
  expect(screen.getByRole('row', { name: /Checks 90 days Check start Never/ })).toBeInTheDocument();
  expect(
    screen.getByRole('row', {
      name: /Incidents 365 days Resolution Open or pending notifications/,
    }),
  ).toBeInTheDocument();
  expect(
    screen.getByText(
      /ENABLING.*Expired records disappear from views immediately and from storage later/,
    ),
  ).toBeInTheDocument();
  expect(screen.getByText(/running; 42 records stamped/)).toBeInTheDocument();
  expect(screen.getByText('127.0.0.1:8090')).toBeInTheDocument();
  expect(screen.getByText('24 h, 7 d')).toBeInTheDocument();
});
it('reports store outage, retries, and keeps previously loaded policy visible when refresh fails', async () => {
  let unavailable = true;
  stubApi({
    'GET /api/system': () =>
      unavailable ? jsonResponse({ error: 'store_unavailable' }, 503) : jsonResponse(system),
  });
  render(<SettingsPage />);
  expect(await screen.findByRole('alert')).toHaveTextContent('backend store is unavailable');
  unavailable = false;
  fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
  expect(await screen.findByRole('heading', { name: 'Retention' })).toBeInTheDocument();
  unavailable = true;
  fireEvent(document, new Event('visibilitychange'));
  await waitFor(() =>
    expect(screen.getByRole('alert')).toHaveTextContent('Showing last available data'),
  );
  expect(screen.getByRole('row', { name: /Checks 90 days/ })).toBeInTheDocument();
});

it('reflects an enabled TTL state and completed backfill after a refresh', async () => {
  let complete = false;
  stubApi({
    'GET /api/system': () =>
      jsonResponse(
        complete
          ? {
              ...system,
              ttl: { status: 'ENABLED', attribute: 'expiresAt' },
              backfill: {
                ...system.backfill,
                state: 'done',
                stamped: 57,
                finishedAt: '2026-09-28T11:00:00.000Z',
              },
              housekeeping: { ...system.housekeeping, pendingRetentionJobs: 0 },
            }
          : system,
      ),
  });
  render(<SettingsPage />);
  await screen.findByText(/ENABLING.*Expired records/);
  complete = true;
  fireEvent(document, new Event('visibilitychange'));
  expect(await screen.findByText(/ENABLED.*Expired records/)).toBeInTheDocument();
  expect(screen.getByText(/done; 57 records stamped/)).toBeInTheDocument();
});
