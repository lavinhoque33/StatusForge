import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { jsonResponse, stubApi } from './test/fixtures';

const READINESS_GET = 'GET /api/health/ready';
const MONITORS_GET = 'GET /api/monitors';

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.pushState(null, '', '/');
});

describe('App', () => {
  it('keeps the backend status visible and navigates between the two top-level entries', async () => {
    stubApi({
      [READINESS_GET]: () =>
        jsonResponse({
          status: 'ready',
          checkedAt: '2026-09-27T12:00:00.000Z',
          dependencies: { dynamodb: { status: 'ready' } },
        }),
      [MONITORS_GET]: () => jsonResponse({ monitors: [] }),
    });

    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Backend status' })).toBeInTheDocument();
    expect(
      await screen.findByText('No monitors yet. Create one to check the sample target.'),
    ).toBeInTheDocument();
    expect(screen.getAllByRole('navigation')).toHaveLength(1);

    fireEvent.click(screen.getByRole('link', { name: 'New monitor' }));

    expect(await screen.findByRole('heading', { name: 'New monitor' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Backend status' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Monitors' })).toHaveAttribute('href', '/');
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
