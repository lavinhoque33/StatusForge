import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { jsonResponse, monitorFixture, observationFixture, stubApi } from '../test/fixtures';
import { MonitorListPage } from './MonitorListPage';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('MonitorListPage', () => {
  it('explains what to do when no monitors exist', async () => {
    stubApi({ 'GET /api/monitors': () => jsonResponse({ monitors: [] }) });

    render(<MonitorListPage />);

    expect(
      await screen.findByText('No monitors yet. Create one to check the sample target.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create a monitor' })).toHaveAttribute(
      'href',
      '/monitors/new',
    );
  });

  it('shows Unknown instead of an outcome for a monitor that has never been checked', async () => {
    stubApi({
      'GET /api/monitors': () =>
        jsonResponse({ monitors: [{ ...monitorFixture(), lastObservation: null }] }),
    });

    render(<MonitorListPage />);

    expect(await screen.findByText('Unknown — no checks yet')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Sample target' })).toHaveAttribute(
      'href',
      '/monitors/monitor-1',
    );
    expect(screen.getByText('Active')).toBeInTheDocument();
    expect(screen.queryByText(/Healthy|Failing|Checker problem/)).not.toBeInTheDocument();
  });

  it('summarises the newest manual check with outcome, reason, age, and local time', async () => {
    const completedAt = new Date(Date.now() - 90_000).toISOString();
    stubApi({
      'GET /api/monitors': () =>
        jsonResponse({
          monitors: [
            {
              ...monitorFixture(),
              lastObservation: observationFixture({ completedAt, startedAt: completedAt }),
            },
          ],
        }),
    });

    render(<MonitorListPage />);

    const headline = await screen.findByText(/Last manual check:/);
    expect(headline).toHaveTextContent(/Last manual check: .*Healthy \(ok\) — 1 min ago/);

    const times = headline.querySelectorAll('time');
    expect(times).toHaveLength(2);
    expect(times[0]).toHaveAttribute('datetime', completedAt);
    expect(times[1]?.getAttribute('title')).toMatch(
      /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}$/,
    );
  });

  it('keeps archived monitors visible with their observations', async () => {
    stubApi({
      'GET /api/monitors': () =>
        jsonResponse({
          monitors: [
            {
              ...monitorFixture({ lifecycle: 'archived', archivedAt: '2026-09-27T11:00:00.000Z' }),
              lastObservation: observationFixture(),
            },
          ],
        }),
    });

    render(<MonitorListPage />);

    expect(await screen.findByText('Archived')).toBeInTheDocument();
    expect(screen.getByText(/Last manual check:/)).toHaveTextContent('Healthy (ok)');
  });

  it('reports an unavailable store and lets the reader try again', async () => {
    let attempts = 0;
    stubApi({
      'GET /api/monitors': () => {
        attempts += 1;
        return attempts === 1
          ? jsonResponse({ error: 'store_unavailable' }, 503)
          : jsonResponse({ monitors: [] });
      },
    });

    render(<MonitorListPage />);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The backend store is unavailable. Try again.',
    );

    screen.getByRole('button', { name: 'Try again' }).click();

    await waitFor(() =>
      expect(
        screen.getByText('No monitors yet. Create one to check the sample target.'),
      ).toBeInTheDocument(),
    );
    expect(attempts).toBe(2);
  });
});
