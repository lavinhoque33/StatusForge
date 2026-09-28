import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { jsonResponse, stubApi } from './test/fixtures';

const READINESS_GET = 'GET /api/health/ready';
const MONITORS_GET = 'GET /api/monitors';
const OVERVIEW_GET = 'GET /api/overview';

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.pushState(null, '', '/');
});

describe('App', () => {
  it('navigates between Monitors and Incidents while keeping backend status visible', async () => {
    stubApi({
      [READINESS_GET]: () =>
        jsonResponse({
          status: 'ready',
          checkedAt: '2026-09-27T12:00:00.000Z',
          dependencies: { dynamodb: { status: 'ready' } },
        }),
      [MONITORS_GET]: () => jsonResponse({ monitors: [] }),
      'GET /api/applications': () => jsonResponse({ applications: [] }),
      [OVERVIEW_GET]: () =>
        jsonResponse({
          evaluatedAt: '2026-09-27T12:00:00.000Z',
          receiveOutages: [],
          openIncidents: [],
          failingWithoutIncident: [],
          coverageProblems: [],
          notifications: { count: 0, items: [] },
          recentRecoveries: [],
          counts: {
            active: 0,
            paused: 0,
            archived: 0,
            byState: { healthy: 0, late: 0, failing: 0, checker_problem: 0, stale: 0, unknown: 0 },
          },
          limits: {
            openIncidents: 50,
            recentRecoveries: 20,
            notifications: 10,
            recoveryWindowHours: 24,
          },
        }),
      'GET /api/notifications/attention?limit=200': () => jsonResponse({ notifications: [] }),
      'GET /api/incidents?state=all&limit=50': () => jsonResponse({ incidents: [] }),
    });

    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Backend status' })).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(
      screen.getByText(/All clear — 0 active monitors checked recently; 0 paused/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('link', { name: 'Monitors' }));
    expect(
      await screen.findByText('No monitors yet. Create one to check the sample target.'),
    ).toBeInTheDocument();
    expect(screen.getAllByRole('navigation')).toHaveLength(1);

    fireEvent.click(screen.getByRole('link', { name: 'Incidents' }));

    expect(await screen.findByRole('heading', { name: 'Open incidents' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Backend status' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Monitors' })).toHaveAttribute('href', '/monitors');
  });

  it('shows a not-found page for an unknown address', async () => {
    window.history.pushState(null, '', '/nope');
    stubApi({
      [READINESS_GET]: () =>
        jsonResponse({
          status: 'ready',
          checkedAt: '2026-09-27T12:00:00.000Z',
          dependencies: { dynamodb: { status: 'ready' } },
        }),
    });

    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
  });
});
