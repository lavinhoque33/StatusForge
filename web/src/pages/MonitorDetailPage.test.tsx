import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  callsTo,
  gapFixture,
  jsonResponse,
  monitorRecordFixture,
  monitorStatusFixture,
  observationFixture,
  stubApi,
} from '../test/fixtures';
import { MonitorDetailPage } from './MonitorDetailPage';

const MONITOR_GET = 'GET /api/monitors/monitor-1';
const OBSERVATIONS_GET = 'GET /api/monitors/monitor-1/observations?limit=50';
const GAPS_GET = 'GET /api/monitors/monitor-1/gaps?limit=50';
const INTERVALS_GET = 'GET /api/intervals';
const CHECKS_POST = 'POST /api/monitors/monitor-1/checks';
const MONITOR_PATCH = 'PATCH /api/monitors/monitor-1';
const LIFECYCLE_POST = 'POST /api/monitors/monitor-1/lifecycle';

type MonitorRecordFixture = ReturnType<typeof monitorRecordFixture>;

/** Read stubs shared by most tests: unknown status, no history, intervals. */
type StubMap = Record<string, (init: RequestInit) => Response>;

function baseStubs(
  overrides: {
    monitor?: MonitorRecordFixture;
    observations?: unknown[];
    gaps?: unknown[];
  } = {},
): StubMap {
  return {
    [MONITOR_GET]: () => jsonResponse(overrides.monitor ?? monitorRecordFixture()),
    [OBSERVATIONS_GET]: () => jsonResponse({ observations: overrides.observations ?? [] }),
    [GAPS_GET]: () => jsonResponse({ gaps: overrides.gaps ?? [] }),
    [INTERVALS_GET]: () =>
      jsonResponse({ intervalSeconds: [60, 300, 600, 900], defaultIntervalSeconds: 300 }),
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('MonitorDetailPage', () => {
  it('states Unknown while no checks have been recorded', async () => {
    stubApi(baseStubs({ monitor: monitorRecordFixture({ configVersion: 2 }) }));

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(await screen.findByText('Unknown — no checks yet')).toBeInTheDocument();
    expect(screen.getByText('No checks have been recorded for this monitor.')).toBeInTheDocument();
    expect(
      screen.queryByText(/Configuration changed since the last check/),
    ).not.toBeInTheDocument();
    expect(screen.getByText('http://127.0.0.1:8090/')).toBeInTheDocument();
    expect(document.querySelectorAll('.version')[0]?.textContent).toBe('v2');
  });

  it('shows a not-found page for a monitor that does not exist', async () => {
    stubApi({
      [MONITOR_GET]: () => jsonResponse({ error: 'monitor_not_found' }, 404),
      [OBSERVATIONS_GET]: () => jsonResponse({ error: 'monitor_not_found' }, 404),
      [GAPS_GET]: () => jsonResponse({ error: 'monitor_not_found' }, 404),
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
          : jsonResponse(monitorRecordFixture());
      },
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [GAPS_GET]: () => jsonResponse({ gaps: [] }),
      [INTERVALS_GET]: () =>
        jsonResponse({ intervalSeconds: [60, 300, 600, 900], defaultIntervalSeconds: 300 }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The backend store is unavailable. Try again.',
    );

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    expect(await screen.findByText('Unknown — no checks yet')).toBeInTheDocument();
    expect(reads).toBe(2);
  });

  it('renders the headline from the presented status with the initiator', async () => {
    const completedAt = new Date(Date.now() - 20_000).toISOString();
    stubApi(
      baseStubs({
        monitor: monitorRecordFixture(
          { intervalSeconds: 60 },
          monitorStatusFixture({
            state: 'healthy',
            reason: null,
            observation: observationFixture({
              initiatedBy: 'scheduled',
              completedAt,
              startedAt: completedAt,
            }),
            freshUntil: new Date(Date.now() + 40_000).toISOString(),
          }),
        ),
      }),
    );

    render(<MonitorDetailPage monitorId="monitor-1" />);

    const headline = await screen.findByText(/Healthy \(ok\)/);
    expect(headline).toHaveTextContent(/checked 20 s ago/);
    expect(headline).toHaveTextContent(/, scheduled/);
  });

  it('merges observations and gaps into one newest-first timeline', async () => {
    const newestStarted = new Date(Date.now() - 30_000).toISOString();
    stubApi(
      baseStubs({
        observations: [
          observationFixture({ id: 'o1', startedAt: newestStarted, completedAt: newestStarted }),
        ],
        gaps: [gapFixture({ id: 'g1' })],
      }),
    );

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(await screen.findByText(/Missed 4 checks/)).toBeInTheDocument();
    expect(screen.getByText(/StatusForge was not running/)).toBeInTheDocument();
  });

  it('runs one check and shows the observation it stored', async () => {
    const completedAt = new Date(Date.now() - 10_000).toISOString();
    let releaseCheck: (response: Response) => void = () => {};
    let checked = false;
    const fetchMock = stubApi({
      [MONITOR_GET]: () =>
        jsonResponse(
          monitorRecordFixture(
            { intervalSeconds: 60 },
            checked
              ? monitorStatusFixture({
                  state: 'healthy',
                  reason: null,
                  observation: observationFixture({
                    completedAt,
                    startedAt: completedAt,
                    counted: true,
                  }),
                  freshUntil: new Date(Date.now() + 60_000).toISOString(),
                })
              : monitorStatusFixture(),
          ),
        ),
      [OBSERVATIONS_GET]: () =>
        jsonResponse({
          observations: checked
            ? [observationFixture({ completedAt, startedAt: completedAt, counted: true })]
            : [],
        }),
      [GAPS_GET]: () => jsonResponse({ gaps: [] }),
      [INTERVALS_GET]: () =>
        jsonResponse({ intervalSeconds: [60, 300, 600, 900], defaultIntervalSeconds: 300 }),
      [CHECKS_POST]: () =>
        new Promise<Response>((resolve) => {
          releaseCheck = (response) => {
            checked = true;
            resolve(response);
          };
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
      releaseCheck(
        jsonResponse(
          observationFixture({ completedAt, startedAt: completedAt, counted: true }),
          201,
        ),
      );
    });
    // The stored observation is counted, so the page refreshes immediately and
    // the headline follows the server's recomputed status.
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(screen.getByRole('button', { name: 'Run check now' })).toBeEnabled();
    const row = screen.getAllByRole('row', { name: /Healthy/ })[0];
    expect(row).toHaveTextContent('200');
    expect(row).toHaveTextContent('12 ms');
    expect(row).toHaveTextContent('v1');
    expect(row).toHaveTextContent('manual');
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/checks')).toHaveLength(1);
  });

  it('keeps focus on Run check now while the check runs and ignores a second activation', async () => {
    let releaseCheck: (response: Response) => void = () => {};
    const fetchMock = stubApi({
      ...baseStubs(),
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

    // The focus restore is a state round-trip; wait for it to flush.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Run check now' })).toHaveFocus(),
    );
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/checks')).toHaveLength(1);
  });

  it('never shows an outcome when the check could not be recorded', async () => {
    stubApi({
      ...baseStubs(),
      [CHECKS_POST]: () => jsonResponse({ error: 'store_unavailable' }, 503),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.click(screen.getByRole('button', { name: 'Run check now' }));

    expect(await screen.findByText('The check ran but could not be recorded.')).toBeInTheDocument();
    expect(screen.getByText('Unknown — no checks yet')).toBeInTheDocument();
    expect(screen.queryByText(/Healthy|Failing|Checker problem/)).not.toBeInTheDocument();
    expect(screen.getByText('No checks have been recorded for this monitor.')).toBeInTheDocument();
  });

  it('reports a check already in progress without retrying it', async () => {
    const fetchMock = stubApi({
      ...baseStubs(),
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
    stubApi({
      [MONITOR_GET]: () =>
        jsonResponse(
          monitorRecordFixture(
            { lifecycle: 'archived', archivedAt: '2026-09-27T11:00:00.000Z' },
            monitorStatusFixture({ state: 'archived', observation: null }),
          ),
        ),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [GAPS_GET]: () => jsonResponse({ gaps: [] }),
      [INTERVALS_GET]: () =>
        jsonResponse({ intervalSeconds: [60, 300, 600, 900], defaultIntervalSeconds: 300 }),
      [CHECKS_POST]: () => jsonResponse({ error: 'archived' }, 409),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Archived', { selector: 'span.lifecycle' });

    expect(screen.getByRole('button', { name: 'Run check now' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Pause' })).not.toBeInTheDocument();
  });

  it('saves configuration changes with the version it loaded', async () => {
    const fetchMock = stubApi({
      ...baseStubs(),
      [MONITOR_PATCH]: () =>
        jsonResponse(monitorRecordFixture({ name: 'Renamed', configVersion: 2 })),
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

  it('sends the chosen interval on save and shows a no-longer-offered stored one', async () => {
    const fetchMock = stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorRecordFixture({ intervalSeconds: 45 })),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [GAPS_GET]: () => jsonResponse({ gaps: [] }),
      [INTERVALS_GET]: () =>
        jsonResponse({ intervalSeconds: [60, 300, 600, 900], defaultIntervalSeconds: 300 }),
      [MONITOR_PATCH]: () => jsonResponse(monitorRecordFixture({ intervalSeconds: 60 })),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);

    // The stored 45 s is not offered any more; it appears selected, marked.
    const selector = await screen.findByLabelText('Check every');
    expect(selector).toHaveValue('45');
    const options = selector.querySelectorAll('option');
    expect(options[0]).toHaveTextContent('(no longer offered)');

    fireEvent.change(selector, { target: { value: '60' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    await screen.findByText('Changes saved.');
    expect(
      JSON.parse(String(callsTo(fetchMock, 'PATCH', '/api/monitors/monitor-1')[0]?.[1]?.body)),
    ).toMatchObject({ intervalSeconds: 60 });
  });

  it('preselects the stored interval when the backend still offers it', async () => {
    stubApi({
      [MONITOR_GET]: () => jsonResponse(monitorRecordFixture({ intervalSeconds: 600 })),
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [GAPS_GET]: () => jsonResponse({ gaps: [] }),
      [INTERVALS_GET]: () =>
        jsonResponse({ intervalSeconds: [60, 300, 600, 900], defaultIntervalSeconds: 300 }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);

    // The stored 600 s (10 min) is offered, so it is selected — not the 300 s
    // default, which is only for creation.
    const selector = await screen.findByLabelText('Check every');
    expect(selector).toHaveValue('600');
    expect(screen.queryByText('(no longer offered)')).not.toBeInTheDocument();
  });

  it('reloads the monitor on a version conflict without retrying the edit', async () => {
    let monitorReads = 0;
    const fetchMock = stubApi({
      [MONITOR_GET]: () => {
        monitorReads += 1;
        return jsonResponse(monitorRecordFixture({ configVersion: monitorReads === 1 ? 1 : 2 }));
      },
      [OBSERVATIONS_GET]: () => jsonResponse({ observations: [] }),
      [GAPS_GET]: () => jsonResponse({ gaps: [] }),
      [INTERVALS_GET]: () =>
        jsonResponse({ intervalSeconds: [60, 300, 600, 900], defaultIntervalSeconds: 300 }),
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
      ...baseStubs(),
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

  it('pauses the monitor and keeps the last result visible', async () => {
    const completedAt = new Date(Date.now() - 30_000).toISOString();
    let paused = false;
    const fetchMock = stubApi({
      ...baseStubs({
        observations: [observationFixture({ completedAt, startedAt: completedAt })],
      }),
      // Lifecycle-aware, like the real backend: a poll that races the pause
      // must not flip the monitor back to active, which would unmount the
      // Resume button the focus assertion targets.
      [MONITOR_GET]: () =>
        jsonResponse(
          paused
            ? monitorRecordFixture(
                { lifecycle: 'paused', pausedAt: '2026-09-27T11:00:00.000Z' },
                monitorStatusFixture({ state: 'paused', observation: null }),
              )
            : monitorRecordFixture(),
        ),
      [LIFECYCLE_POST]: () => {
        paused = true;
        return jsonResponse(
          monitorRecordFixture(
            { lifecycle: 'paused', pausedAt: '2026-09-27T11:00:00.000Z' },
            monitorStatusFixture({ state: 'paused', observation: null }),
          ),
        );
      },
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.click(screen.getByRole('button', { name: 'Pause' }));

    expect(await screen.findByText('Monitor paused.')).toBeInTheDocument();
    expect(screen.getByText('Paused', { selector: 'span.lifecycle' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Resume' })).toBeInTheDocument();
    // Focus is applied in a passive effect after React commits; waitFor lets
    // that flush land instead of racing it synchronously.
    await waitFor(() => expect(screen.getByRole('button', { name: 'Resume' })).toHaveFocus());
    expect(
      JSON.parse(
        String(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/lifecycle')[0]?.[1]?.body),
      ),
    ).toEqual({ action: 'pause' });
  });

  it('asks for confirmation before archiving', async () => {
    const fetchMock = stubApi({
      ...baseStubs(),
      [LIFECYCLE_POST]: () =>
        jsonResponse(
          monitorRecordFixture(
            { lifecycle: 'archived', archivedAt: '2026-09-27T11:00:00.000Z' },
            monitorStatusFixture({ state: 'archived', observation: null }),
          ),
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
    const fetchMock = stubApi(baseStubs());

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await screen.findByText('Unknown — no checks yet');

    fireEvent.click(screen.getByRole('button', { name: 'Archive' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(screen.queryByRole('button', { name: 'Confirm archive' })).not.toBeInTheDocument();
    // Focus returns to the Archive button that opened the confirm flow, via
    // the passive focus effect; waitFor lets that flush land.
    await waitFor(() => expect(screen.getByRole('button', { name: 'Archive' })).toHaveFocus());
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/lifecycle')).toHaveLength(0);
  });

  it('keeps an archived monitor readable with every mutation disabled', async () => {
    stubApi(
      baseStubs({
        monitor: monitorRecordFixture(
          { lifecycle: 'archived', archivedAt: '2026-09-27T11:00:00.000Z' },
          monitorStatusFixture({ state: 'archived', observation: observationFixture() }),
        ),
        observations: [observationFixture()],
      }),
    );

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(await screen.findByText('Archived', { selector: 'span.lifecycle' })).toBeInTheDocument();
    expect(
      screen.getByText('Archived monitors are read-only. Observations stay available.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Run check now' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    expect(screen.getByLabelText('Name')).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Pause' })).not.toBeInTheDocument();
    expect(screen.getByRole('row', { name: /Healthy/ })).toHaveTextContent('v1');
  });

  it('notes configuration drift above the timeline and clears it after a check at the new version', async () => {
    stubApi(
      baseStubs({
        monitor: monitorRecordFixture({ configVersion: 4 }),
        observations: [observationFixture({ configVersion: 3 })],
      }),
    );

    render(<MonitorDetailPage monitorId="monitor-1" />);

    expect(
      await screen.findByText('Configuration changed since the last check (v3 → v4).'),
    ).toBeInTheDocument();
  });

  it('shows Not updated and keeps data when a poll fails, and never re-runs a mutation', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T12:00:00.000Z'));
    let fail = false;
    let checkRuns = 0;
    stubApi({
      [MONITOR_GET]: () => {
        if (fail) return jsonResponse({ error: 'store_unavailable' }, 503);
        return jsonResponse(
          monitorRecordFixture(
            { intervalSeconds: 60 },
            monitorStatusFixture({
              state: 'healthy',
              reason: null,
              observation: observationFixture({ counted: true }),
              freshUntil: new Date(Date.now() + 600_000).toISOString(),
            }),
          ),
        );
      },
      [OBSERVATIONS_GET]: () => {
        if (fail) return jsonResponse({ error: 'store_unavailable' }, 503);
        return jsonResponse({ observations: [observationFixture({ counted: true })] });
      },
      [GAPS_GET]: () => jsonResponse({ gaps: [] }),
      [INTERVALS_GET]: () =>
        jsonResponse({ intervalSeconds: [60, 300, 600, 900], defaultIntervalSeconds: 300 }),
      [CHECKS_POST]: () => {
        checkRuns += 1;
        return jsonResponse(observationFixture({ counted: true }), 201);
      },
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30);
    });
    expect(screen.getByText(/Healthy \(ok\)/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Run check now' }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30);
    });
    expect(checkRuns).toBe(1);

    fail = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });

    // The observation the user's check stored stays on the page.
    expect(screen.getAllByRole('row', { name: /Healthy/ }).length).toBeGreaterThan(0);
    expect(
      screen.getByText('Not updated — The backend store is unavailable. Try again.'),
    ).toBeInTheDocument();
    expect(checkRuns).toBe(1);
  });

  it('keeps polling while the edit form is dirty but never rewrites the form', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T12:00:00.000Z'));
    let reads = 0;
    stubApi({
      [MONITOR_GET]: () => {
        reads += 1;
        return jsonResponse(
          monitorRecordFixture(
            { intervalSeconds: 60 },
            monitorStatusFixture({
              state: 'failing',
              reason: null,
              observation: observationFixture({
                outcome: 'failing',
                reason: 'wrong_status',
                observedStatus: 500,
                counted: true,
              }),
              freshUntil: new Date(Date.now() + 600_000).toISOString(),
            }),
          ),
        );
      },
      [OBSERVATIONS_GET]: () => {
        reads += 1;
        return jsonResponse({ observations: [] });
      },
      [GAPS_GET]: () => jsonResponse({ gaps: [] }),
      [INTERVALS_GET]: () =>
        jsonResponse({ intervalSeconds: [60, 300, 600, 900], defaultIntervalSeconds: 300 }),
    });

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30);
    });
    expect(screen.getByLabelText('Name')).toBeInTheDocument();
    const readsAfterLoad = reads;

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Typing…' } });

    // The poll keeps running while dirty (status/observations/gaps stay live).
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(reads).toBeGreaterThan(readsAfterLoad);
    // The typed value survives every poll: the form is never rewritten.
    expect(screen.getByLabelText('Name')).toHaveValue('Typing…');
  });

  it('refreshes the relative age on a timer', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T12:00:00.000Z'));
    stubApi(
      baseStubs({
        monitor: monitorRecordFixture(
          { intervalSeconds: 900 },
          monitorStatusFixture({
            state: 'healthy',
            reason: null,
            observation: observationFixture({
              initiatedBy: 'scheduled',
              startedAt: '2026-09-27T11:59:50.000Z',
              completedAt: '2026-09-27T11:59:50.000Z',
            }),
            freshUntil: '2026-09-27T12:29:50.000Z',
          }),
        ),
      }),
    );

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30);
    });

    expect(screen.getByText(/Healthy \(ok\)/)).toHaveTextContent('10 s ago');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });

    expect(screen.getByText(/Healthy \(ok\)/)).toHaveTextContent('40 s ago');
  });

  it('flips to Stale at exactly freshUntil, not at the next age tick', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T12:00:00.000Z'));
    stubApi(
      baseStubs({
        monitor: monitorRecordFixture(
          { intervalSeconds: 900 },
          monitorStatusFixture({
            state: 'healthy',
            reason: null,
            observation: observationFixture({
              initiatedBy: 'scheduled',
              startedAt: '2026-09-27T11:59:50.000Z',
              completedAt: '2026-09-27T11:59:50.000Z',
              counted: true,
            }),
            freshUntil: '2026-09-27T12:00:31.000Z',
          }),
        ),
      }),
    );

    render(<MonitorDetailPage monitorId="monitor-1" />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30);
    });
    expect(screen.getByText(/Healthy \(ok\)/)).toBeInTheDocument();

    // 31 s after load: freshUntil (12:00:31) has just passed. The 30 s age
    // tick alone would not have re-rendered past the switch.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(31_200);
    });

    expect(screen.getByText(/Stale — last result Healthy/)).toBeInTheDocument();
  });
});
