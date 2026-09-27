import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  callsTo,
  jsonResponse,
  monitorFixture,
  observationFixture,
  stubApi,
} from '../test/fixtures';
import { MonitorDetailPage } from './MonitorDetailPage';

const MONITOR_GET = 'GET /api/monitors/monitor-1';
const OBSERVATIONS_GET = 'GET /api/monitors/monitor-1/observations?limit=50';
const CHECKS_POST = 'POST /api/monitors/monitor-1/checks';
const MONITOR_PATCH = 'PATCH /api/monitors/monitor-1';
const LIFECYCLE_POST = 'POST /api/monitors/monitor-1/lifecycle';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('MonitorDetailPage', () => {
  it('states Unknown while no checks have been recorded', async () => {
    stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture({ configVersion: 2 })),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(await screen.findByText('Unknown — no checks yet')).toBeInTheDocument();
    expect(screen.getByText('No checks have been recorded for this monitor.')).toBeInTheDocument();
    expect(
      screen.queryByText(/Configuration changed since the last check/),
    ).not.toBeInTheDocument();
    expect(screen.getByText('http://127.0.0.1:8090/')).toBeInTheDocument();
    expect(screen.getAllByText('v2').length).toBeGreaterThan(0);
  });

  it('shows a not-found page for a monitor that does not exist', async () => {
    stubApi({
      [MONITOR_GET]: () => jsonResponse({ error: 'monitor_not_found' }, 404),
      [OBSERVATIONS_GET]: () => jsonResponse({ error: 'monitor_not_found' }, 404),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(await screen.findByRole('heading', { name: 'Monitor not found' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Back to monitors' })).toHaveAttribute('href', '/');
  });

  it('offers a retry when the monitor could not be loaded', async () => {
    let reads = 0;
    stubApi({
      [MONITOR_GET]: () => {
        reads += 1;
        return reads === 1
          ? jsonResponse({ error: 'store_unavailable' }, 503)
          : jsonResponse(monitorFixture());
      },
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The backend store is unavailable. Try again.',
    );

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    expect(await screen.findByText('Unknown — no checks yet')).toBeInTheDocument();
    expect(reads).toBe(2);
  });

  it('runs one check and shows the observation it stored', async () => {
    const completedAt = new Date(Date.now() - 10_000).toISOString();
    let releaseCheck: (response: Response) => void = () => {};
    const fetchMock = stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture()),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [CHECKS_POST]: () =>
        new Promise<Response>((resolve) => {
          releaseCheck = resolve;
        }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.click(screen.getByRole('button', { name: 'Run check now' }));

    expect(screen.getByRole('button', { name: 'Checking… up to 10 s' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );

    await act(async () => {
      releaseCheck(jsonResponse(observationFixture({ completedAt, startedAt: completedAt }), 201));
    });

    expect(screen.getByRole('button', { name: 'Run check now' })).toBeEnabled();
    expect(screen.getByText(/Last manual check:/)).toHaveTextContent(/Healthy \(ok\) — 10 s ago/);

    const row = screen.getByRole('row', { name: /Healthy/ });
    expect(row).toHaveTextContent('200');
    expect(row).toHaveTextContent('12 ms');
    expect(row).toHaveTextContent('v1');
    expect(row).toHaveTextContent('manual');
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/checks')).toHaveLength(1);
  });

  it('keeps focus on Run check now while the check runs and ignores a second activation', async () => {
    let releaseCheck: (response: Response) => void = () => {};
    const fetchMock = stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture()),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [CHECKS_POST]: () =>
        new Promise<Response>((resolve) => {
          releaseCheck = resolve;
        }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    const idleButton = screen.getByRole('button', { name: 'Run check now' });
    idleButton.focus();
    fireEvent.click(idleButton);

    // Focusable but not activatable: a keyboard user keeps their place, and Tab
    // still reaches the lifecycle actions.
    const pendingButton = screen.getByRole('button', { name: 'Checking… up to 10 s' });
    expect(pendingButton).toHaveFocus();
    expect(pendingButton).toHaveAttribute('aria-disabled', 'true');
    expect(pendingButton).not.toBeDisabled();

    fireEvent.click(pendingButton);
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/checks')).toHaveLength(1);

    await act(async () => {
      releaseCheck(jsonResponse(observationFixture(), 201));
    });

    expect(screen.getByRole('button', { name: 'Run check now' })).toHaveFocus();
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/checks')).toHaveLength(1);
  });

  it('never shows an outcome when the check could not be recorded', async () => {
    let reads = 0;
    stubApi({
      [MONITOR_GET]: () => {
        reads += 1;
        return jsonResponse(monitorFixture());
      },
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [CHECKS_POST]: () => jsonResponse({ error: 'store_unavailable' }, 503),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.click(screen.getByRole('button', { name: 'Run check now' }));

    expect(await screen.findByText('The check ran but could not be recorded.')).toBeInTheDocument();
    expect(screen.getByText('Unknown — no checks yet')).toBeInTheDocument();
    expect(screen.queryByText(/Healthy|Failing|Checker problem/)).not.toBeInTheDocument();
    expect(screen.getByText('No checks have been recorded for this monitor.')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));

    await waitFor(() => expect(reads).toBe(2));
    expect(screen.queryByText('The check ran but could not be recorded.')).not.toBeInTheDocument();
    expect(screen.getByText('Unknown — no checks yet')).toBeInTheDocument();
  });

  it('reports a check already in progress without retrying it', async () => {
    const fetchMock = stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture()),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [CHECKS_POST]: () => jsonResponse({ error: 'check_in_progress' }, 409),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.click(screen.getByRole('button', { name: 'Run check now' }));

    expect(await screen.findByText('A check is already running.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Run check now' })).toBeEnabled();
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/checks')).toHaveLength(1);
  });

  it('re-reads a monitor that was archived elsewhere and disables its actions', async () => {
    let monitorReads = 0;
    stubApi({
      [MONITOR_GET]: () => {
        monitorReads += 1;
        return jsonResponse(
          monitorReads === 1
            ? monitorFixture()
            : monitorFixture({ lifecycle: 'archived', archivedAt: '2026-09-27T11:00:00.000Z' }),
        );
      },
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [CHECKS_POST]: () => jsonResponse({ error: 'archived' }, 409),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.click(screen.getByRole('button', { name: 'Run check now' }));

    expect(
      await screen.findByText('This monitor is archived; checks are not allowed.'),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Run check now' })).toBeDisabled(),
    );
    expect(screen.getByText('Archived', { selector: 'span.lifecycle' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Pause' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument();
  });

  it('saves configuration changes with the version it loaded', async () => {
    const fetchMock = stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture()),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [MONITOR_PATCH]: () => jsonResponse(monitorFixture({ name: 'Renamed', configVersion: 2 })),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Renamed' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByText('Changes saved.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Renamed' })).toBeInTheDocument();
    expect(
      JSON.parse(String(callsTo(fetchMock, 'PATCH', '/api/monitors/monitor-1')[0]?.[1]?.body)),
    ).toEqual({
      expectedConfigVersion: 1,
      name: 'Renamed',
      check: { url: 'http://127.0.0.1:8090/', expectedStatus: 200, deadlineMs: 10000 },
    });
  });

  it('reloads the monitor on a version conflict without retrying the edit', async () => {
    let monitorReads = 0;
    const fetchMock = stubApi({
      [MONITOR_GET]: () => {
        monitorReads += 1;
        return jsonResponse(monitorFixture({ configVersion: monitorReads === 1 ? 1 : 2 }));
      },
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [MONITOR_PATCH]: () => jsonResponse({ error: 'version_conflict' }, 409),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Renamed' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(
      await screen.findByText('This monitor changed; review and try again (now v2).'),
    ).toBeInTheDocument();
    expect(callsTo(fetchMock, 'PATCH', '/api/monitors/monitor-1')).toHaveLength(1);
    expect(screen.getByLabelText('Name')).toHaveValue('Renamed');
    await waitFor(() => expect(monitorReads).toBe(2));
  });

  it('renders edit-form validation errors next to the input they belong to', async () => {
    stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture()),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [MONITOR_PATCH]: () =>
        jsonResponse(
          {
            error: 'validation_failed',
            fields: {
              'check.deadlineMs': {
                code: 'out_of_range',
                message: 'deadlineMs must be between 1000 and 30000',
              },
            },
          },
          400,
        ),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.change(screen.getByLabelText('Deadline (seconds)'), { target: { value: '99' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByText('deadlineMs must be between 1000 and 30000')).toHaveAttribute(
      'id',
      'monitor-deadline-seconds-error',
    );
    expect(screen.getByLabelText('Deadline (seconds)').getAttribute('aria-describedby')).toContain(
      'monitor-deadline-seconds-error',
    );
  });

  it('pauses the monitor and keeps the last observation visible', async () => {
    const completedAt = new Date(Date.now() - 30_000).toISOString();
    const fetchMock = stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture()),
      [OBSERVATIONS_GET]: () =>
        jsonResponse({
          observations: [observationFixture({ completedAt, startedAt: completedAt })],
        }),
      [LIFECYCLE_POST]: () =>
        jsonResponse(monitorFixture({ lifecycle: 'paused', pausedAt: '2026-09-27T11:00:00.000Z' })),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    expect(await screen.findByText(/Last manual check:/)).toHaveTextContent('Healthy (ok)');

    fireEvent.click(screen.getByRole('button', { name: 'Pause' }));

    expect(await screen.findByText('Monitor paused.')).toBeInTheDocument();
    expect(screen.getByText('Paused', { selector: 'span.lifecycle' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Resume' })).toBeInTheDocument();
    expect(screen.getByText(/Last manual check:/)).toHaveTextContent('Healthy (ok)');
    expect(
      JSON.parse(
        String(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/lifecycle')[0]?.[1]?.body),
      ),
    ).toEqual({ action: 'pause' });
  });

  it('asks for confirmation before archiving', async () => {
    const fetchMock = stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture()),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [LIFECYCLE_POST]: () =>
        jsonResponse(
          monitorFixture({ lifecycle: 'archived', archivedAt: '2026-09-27T11:00:00.000Z' }),
        ),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.click(screen.getByRole('button', { name: 'Archive' }));

    expect(screen.getByText(/Archiving is final/)).toBeInTheDocument();
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/lifecycle')).toHaveLength(0);

    fireEvent.click(screen.getByRole('button', { name: 'Confirm archive' }));

    expect(
      await screen.findByText('Monitor archived. Observations stay available.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Run check now' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument();
  });

  it('cancels the archive confirmation without calling the backend', async () => {
    const fetchMock = stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture()),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.click(screen.getByRole('button', { name: 'Archive' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(screen.queryByRole('button', { name: 'Confirm archive' })).not.toBeInTheDocument();
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/lifecycle')).toHaveLength(0);
  });

  it('keeps an archived monitor readable with every mutation disabled', async () => {
    stubApi({
      [MONITOR_GET]: () =>
        jsonResponse(
          monitorFixture({ lifecycle: 'archived', archivedAt: '2026-09-27T11:00:00.000Z' }),
        ),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [observationFixture()] }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(await screen.findByText('Archived', { selector: 'span.lifecycle' })).toBeInTheDocument();
    expect(
      screen.getByText('Archived monitors are read-only. Observations stay available.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Run check now' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    expect(screen.getByLabelText('Name')).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Pause' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resume' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument();
    expect(screen.getByRole('row', { name: /Healthy/ })).toHaveTextContent('v1');
  });

  it('notes configuration drift above the table and clears it after a check at the new version', async () => {
    stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture({ configVersion: 4 })),
      [OBSERVATIONS_GET]: () =>
        jsonResponse({ observations: [observationFixture({ configVersion: 3 })] }),
      [CHECKS_POST]: () =>
        jsonResponse(observationFixture({ id: 'observation-2', configVersion: 4 }), 201),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(
      await screen.findByText('Configuration changed since the last check (v3 → v4).'),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Run check now' }));

    await waitFor(() =>
      expect(
        screen.queryByText(/Configuration changed since the last check/),
      ).not.toBeInTheDocument(),
    );
  });

  it('refreshes the relative age on a timer', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T10:16:00.000Z'));
    stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorFixture()),
      [OBSERVATIONS_GET]: () =>
        jsonResponse({
          observations: [
            observationFixture({
              startedAt: '2026-09-27T10:15:00.000Z',
              completedAt: '2026-09-27T10:15:10.000Z',
            }),
          ],
        }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });

    expect(screen.getByText(/Last manual check:/)).toHaveTextContent('50 s ago');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });

    expect(screen.getByText(/Last manual check:/)).toHaveTextContent('1 min ago');
  });
});
