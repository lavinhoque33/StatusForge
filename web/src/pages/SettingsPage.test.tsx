import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { jsonResponse, stubApi, systemFixture } from '../test/fixtures';
import { SettingsPage } from './SettingsPage';

const system = systemFixture();
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
