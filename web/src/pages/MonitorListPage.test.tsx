import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  jsonResponse,
  monitorRecordFixture,
  monitorStatusFixture,
  observationFixture,
  stubApi,
} from '../test/fixtures';
import { MonitorListPage } from './MonitorListPage';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  setHidden(false);
});

/** Fake the tab's visibility for the polling tests. */
function setHidden(hidden: boolean): void {
  Object.defineProperty(document, 'visibilityState', {
    value: hidden ? 'hidden' : 'visible',
    configurable: true,
  });
  Object.defineProperty(document, 'hidden', { value: hidden, configurable: true });
}

/**
 * Let the initial load effect's promise chain settle under fake timers without
 * firing the poll interval: `flushPromises` drains microtasks and short real
 * timeouts, leaving the first `GET /api/monitors` rendered.
 */
async function flushLoad(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
}

/** Drain the promise chain of one poll after its interval fired. */
async function flushSettle(): Promise<void> {
  await vi.advanceTimersByTimeAsync(20);
}

/** An instant `ageMs` before the current (possibly faked) clock. */
function agoIso(ageMs: number): string {
  return new Date(Date.now() - ageMs).toISOString();
}

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
      'GET /api/monitors': () => jsonResponse({ monitors: [monitorRecordFixture()] }),
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

  it('summarises the current status with outcome, reason, age, local time, and initiator', async () => {
    const completedAt = agoIso(90_000);
    stubApi({
      'GET /api/monitors': () =>
        jsonResponse({
          monitors: [
            monitorRecordFixture(
              {},
              monitorStatusFixture({
                state: 'healthy',
                reason: null,
                observation: observationFixture({
                  initiatedBy: 'scheduled',
                  completedAt,
                  startedAt: completedAt,
                }),
                freshUntil: agoIso(-600_000),
              }),
            ),
          ],
        }),
    });

    render(<MonitorListPage />);

    const headline = await screen.findByText(/Healthy \(ok\)/);
    expect(headline).toHaveTextContent(/checked 1 min ago/);
    expect(headline).toHaveTextContent(/, scheduled/);
    const times = headline.querySelectorAll('time');
    expect(times).toHaveLength(1);
    expect(times[0]).toHaveAttribute('datetime', completedAt);
    expect(times[0]?.getAttribute('title')).toMatch(
      /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}$/,
    );
  });

  it('shows maintenance as text alongside status without converting unknown to healthy', async () => {
    const window = {
      id: 'w1',
      monitorId: 'monitor-1',
      startAt: '2026-09-27T10:00:00.000Z',
      endAt: '2026-09-27T12:00:00.000Z',
      note: '',
      createdAt: '2026-09-27T09:00:00.000Z',
      cancelledAt: null,
      state: 'active' as const,
    };
    stubApi({
      'GET /api/monitors': () =>
        jsonResponse({
          monitors: [monitorRecordFixture({ maintenance: { active: window, next: null } })],
        }),
    });
    render(<MonitorListPage />);
    const headline = await screen.findByText(/Unknown — no checks yet — in maintenance until/);
    expect(headline.querySelector('time')).toHaveAttribute('datetime', window.endAt);
    expect(headline).not.toHaveTextContent('Healthy');
  });

  it('keeps archived monitors visible with their last result', async () => {
    stubApi({
      'GET /api/monitors': () =>
        jsonResponse({
          monitors: [
            monitorRecordFixture(
              { lifecycle: 'archived', archivedAt: '2026-09-27T11:00:00.000Z' },
              monitorStatusFixture({
                state: 'archived',
                observation: observationFixture(),
              }),
            ),
          ],
        }),
    });

    render(<MonitorListPage />);

    expect(await screen.findByText('Archived', { selector: 'span.lifecycle' })).toBeInTheDocument();
    expect(screen.getByText('Archived', { selector: 'p' })).toBeInTheDocument();
  });

  it('switches to Stale locally once freshUntil passes, without waiting for a poll', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T12:00:00.000Z'));
    stubApi({
      'GET /api/monitors': () =>
        jsonResponse({
          monitors: [
            monitorRecordFixture(
              { intervalSeconds: 60 },
              monitorStatusFixture({
                state: 'healthy',
                reason: null,
                observation: observationFixture({
                  initiatedBy: 'scheduled',
                  completedAt: '2026-09-27T11:57:00.000Z',
                  startedAt: '2026-09-27T11:57:00.000Z',
                }),
                freshUntil: '2026-09-27T12:00:30.000Z',
              }),
            ),
          ],
        }),
    });

    render(<MonitorListPage />);
    await flushLoad();
    expect(screen.getByText(/Healthy \(ok\)/)).toBeInTheDocument();

    // No poll result changes the state; the headline goes stale locally at
    // 12:00:31, one second past freshUntil. The age timer ticks at 30 s and
    // 60 s; 61 s guarantees a re-render past the switch instant.
    await vi.advanceTimersByTimeAsync(61_000);

    expect(screen.getByText(/Stale — last result Healthy/)).toBeInTheDocument();
    expect(screen.getByText(/expected every 1 min/)).toBeInTheDocument();
  });

  it('polls every 15 s while visible and stops while hidden', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T12:00:00.000Z'));
    let reads = 0;
    stubApi({
      'GET /api/monitors': () => {
        reads += 1;
        return jsonResponse({ monitors: [monitorRecordFixture()] });
      },
    });

    render(<MonitorListPage />);
    await flushLoad();
    expect(reads).toBe(1);

    await vi.advanceTimersByTimeAsync(15_000);
    await flushSettle();
    expect(reads).toBe(2);
    await vi.advanceTimersByTimeAsync(15_000);
    await flushSettle();
    expect(reads).toBe(3);

    // Hidden: the interval fires but no request is made.
    setHidden(true);
    await vi.advanceTimersByTimeAsync(45_000);
    expect(reads).toBe(3);

    // Becoming visible refreshes once, immediately.
    setHidden(false);
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(20);
    expect(reads).toBe(4);
  });

  it('ages the Updated marker while no poll runs', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T12:00:00.000Z'));
    stubApi({
      'GET /api/monitors': () => jsonResponse({ monitors: [monitorRecordFixture()] }),
    });

    render(<MonitorListPage />);
    await flushLoad();
    expect(screen.getByText('Updated just now')).toBeInTheDocument();

    // Hidden: no further poll succeeds, so the marker must age. The marker
    // re-renders on the poll interval's no-op ticks and the age timer.
    setHidden(true);
    await vi.advanceTimersByTimeAsync(15_000);
    await vi.advanceTimersByTimeAsync(15_000);
    await vi.advanceTimersByTimeAsync(0);

    expect(screen.getByText('Updated 30 s ago')).toBeInTheDocument();
  });

  it('keeps the last data and reports Not updated when a poll fails', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T12:00:00.000Z'));
    let fail = false;
    stubApi({
      'GET /api/monitors': () => {
        if (fail) return jsonResponse({ error: 'store_unavailable' }, 503);
        return jsonResponse({ monitors: [monitorRecordFixture()] });
      },
    });

    render(<MonitorListPage />);
    await flushLoad();

    fail = true;
    await vi.advanceTimersByTimeAsync(15_000);
    await vi.advanceTimersByTimeAsync(0);

    expect(screen.getByText('Unknown — no checks yet')).toBeInTheDocument();
    expect(
      screen.getByText('Not updated — The backend store is unavailable. Try again.'),
    ).toBeInTheDocument();
    expect(screen.queryByText('Loading monitors…')).not.toBeInTheDocument();
  });

  it('reports an unavailable store before any data exists and lets the reader try again', async () => {
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

it('marks a monitor under deletion without offering mutation controls', async () => {
  stubApi({
    'GET /api/monitors': () =>
      jsonResponse({
        monitors: [
          monitorRecordFixture({
            lifecycle: 'archived',
            archivedAt: '2026-09-27T10:00:00.000Z',
            deletion: {
              state: 'waiting_for_notifications',
              requestedAt: '2026-09-27T10:00:00.000Z',
              updatedAt: '2026-09-27T10:00:00.000Z',
              removedItems: 0,
            },
          }),
        ],
      }),
  });
  render(<MonitorListPage />);
  expect(await screen.findByText('Deleting')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Run check now' })).not.toBeInTheDocument();
});
