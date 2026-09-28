import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { jsonResponse, stubApi, systemFixture } from './test/fixtures';

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

    const incidentsHeading = screen.getByRole('heading', { name: 'Incidents' });
    await waitFor(() => expect(incidentsHeading).toHaveFocus());
    expect(incidentsHeading).toHaveAttribute('tabindex', '-1');
    expect(document.title).toBe('Incidents — StatusForge');
    fireEvent.click(screen.getByRole('link', { name: 'Monitors' }));
    const monitorsHeading = screen.getByRole('heading', { name: 'Monitors' });
    await waitFor(() => expect(monitorsHeading).toHaveFocus());
    expect(document.title).toBe('Monitors — StatusForge');
    window.history.pushState(null, '', '/incidents');
    act(() => window.dispatchEvent(new PopStateEvent('popstate')));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Incidents' })).toHaveFocus());
    expect(document.title).toBe('Incidents — StatusForge');
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

  it.each([
    { demo: true, version: 'v0.6.0', banner: true },
    { demo: false, version: 'v0.6.0', banner: false },
  ])('renders release identity with demo=$demo', async ({ demo, version, banner }) => {
    window.history.pushState(null, '', '/nope');
    stubApi({
      'GET /api/system': () => jsonResponse(systemFixture({ demo, version })),
    });
    render(<App />);
    expect(await screen.findByText(`Version ${version}`)).toBeInTheDocument();
    expect(screen.queryByText('Demo data') !== null).toBe(banner);
    expect(screen.getByRole('contentinfo')).toBeInTheDocument();
  });

  it('does not imply demo data or invent a version when system is unavailable', async () => {
    window.history.pushState(null, '', '/nope');
    stubApi({ 'GET /api/system': () => jsonResponse({ error: 'store_unavailable' }, 503) });
    render(<App />);
    expect(await screen.findByText('Version unavailable')).toBeInTheDocument();
    expect(screen.queryByText('Demo data')).not.toBeInTheDocument();
  });
});
