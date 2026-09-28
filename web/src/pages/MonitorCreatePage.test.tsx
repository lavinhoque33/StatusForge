import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { callsTo, jsonResponse, monitorRecordFixture, stubApi } from '../test/fixtures';
import { MonitorCreatePage } from './MonitorCreatePage';

const INTERVALS_GET = 'GET /api/intervals';

const INTERVALS_OK = {
  [INTERVALS_GET]: () =>
    jsonResponse({
      intervalSeconds: [60, 300, 600, 900],
      defaultIntervalSeconds: 300,
      heartbeat: {
        intervalSeconds: [300, 900, 3600],
        graceSeconds: [60, 300, 900],
        defaultIntervalSeconds: 3600,
        defaultGraceSeconds: 900,
      },
    }),
};

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.pushState(null, '', '/');
  vi.useRealTimers();
});

describe('MonitorCreatePage', () => {
  it('prefills the sample target and the contract defaults', () => {
    stubApi({ ...INTERVALS_OK });

    render(<MonitorCreatePage />);

    expect(screen.getByLabelText('Name')).toHaveValue('');
    expect(screen.getByLabelText('URL')).toHaveValue('http://127.0.0.1:8090/');
    expect(screen.getByLabelText('Expected status')).toHaveValue(200);
    expect(screen.getByLabelText('Deadline (seconds)')).toHaveValue(10);
  });

  it('offers the backend intervals with the default selected', async () => {
    stubApi({ ...INTERVALS_OK });

    render(<MonitorCreatePage />);

    const selector = await screen.findByLabelText('Check every');
    expect(selector).toHaveValue('300');
    const labels = Array.from(selector.querySelectorAll('option')).map((o) => o.textContent);
    expect(labels).toEqual(['1 min', '5 min', '10 min', '15 min']);
  });

  it('sends seconds as deadlineMs, the chosen interval, and opens the created monitor', async () => {
    const fetchMock = stubApi({
      ...INTERVALS_OK,
      'POST /api/monitors': () => jsonResponse(monitorRecordFixture({ id: 'monitor-9' }), 201),
    });

    render(<MonitorCreatePage />);
    const selector = await screen.findByLabelText('Check every');
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Local health' } });
    fireEvent.change(screen.getByLabelText('Deadline (seconds)'), { target: { value: '1.5' } });
    fireEvent.change(selector, { target: { value: '60' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create monitor' }));
      await Promise.resolve();
    });

    await waitFor(() => expect(window.location.pathname).toBe('/monitors/monitor-9'));
    expect(JSON.parse(String(callsTo(fetchMock, 'POST', '/api/monitors')[0]?.[1]?.body))).toEqual({
      name: 'Local health',
      check: { url: 'http://127.0.0.1:8090/', expectedStatus: 200, deadlineMs: 1500 },
      intervalSeconds: 60,
      incidentPolicy: { openAfter: 2, recoverAfter: 2 },
    });
  });

  it('filters grace by interval and shows the issued token only until navigation', async () => {
    const issuedToken = 'sfh_secret-once';
    const heartbeat = monitorRecordFixture(
      {
        id: 'heartbeat-1',
        kind: 'heartbeat',
        check: null,
        intervalSeconds: null,
        heartbeat: {
          intervalSeconds: 300,
          graceSeconds: 300,
          token: { hint: 'once', createdAt: '2026-09-27T10:00:00.000Z' },
          lastReportAt: null,
          ingestPath: '/ingest/heartbeats/heartbeat-1',
        },
        expectation: {
          dueAt: '2026-09-27T10:05:00.000Z',
          lateAt: '2026-09-27T10:05:00.000Z',
          missingAt: '2026-09-27T10:10:00.000Z',
          staleAt: '2026-09-27T10:10:35.000Z',
        },
      },
      { reason: 'waiting_for_first_report' },
    );
    const fetchMock = stubApi({
      ...INTERVALS_OK,
      'POST /api/monitors': () => jsonResponse({ ...heartbeat, issuedToken }, 201),
    });
    const { unmount } = render(<MonitorCreatePage />);
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'heartbeat' } });
    const interval = await screen.findByLabelText('Heartbeat interval');
    fireEvent.change(interval, { target: { value: '300' } });
    const grace = screen.getByLabelText('Grace period');
    expect(Array.from(grace.querySelectorAll('option')).map((option) => option.value)).toEqual([
      '60',
      '300',
    ]);
    expect(grace).toHaveValue('300');
    fireEvent.click(screen.getByRole('button', { name: 'Create monitor' }));
    expect(await screen.findByText('sfh_secret-once')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Copy token' })).toHaveFocus());
    expect(screen.getByText('Heartbeat created. Token shown once.')).toHaveAttribute(
      'role',
      'status',
    );
    expect(
      screen.getByRole('heading', { name: 'Heartbeat token — Shown once' }),
    ).toBeInTheDocument();
    expect(
      screen.getByText((text) =>
        text.includes(
          'curl -fsS -X POST -H "Authorization: Bearer sfh_secret-once" http://127.0.0.1:8080/ingest/heartbeats/heartbeat-1',
        ),
      ),
    ).toBeInTheDocument();
    expect(JSON.parse(String(callsTo(fetchMock, 'POST', '/api/monitors')[0]?.[1]?.body))).toEqual({
      kind: 'heartbeat',
      name: '',
      heartbeat: { intervalSeconds: 300, graceSeconds: 300 },
      incidentPolicy: { openAfter: 1, recoverAfter: 1 },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    unmount();
    render(<MonitorCreatePage />);
    expect(screen.queryByText(issuedToken)).not.toBeInTheDocument();
  });

  it('sends no interval when /api/intervals is unavailable, letting the backend default apply', async () => {
    const fetchMock = stubApi({
      [INTERVALS_GET]: () => jsonResponse({ error: 'store_unavailable' }, 503),
      'POST /api/monitors': () => jsonResponse(monitorRecordFixture({ id: 'monitor-9' }), 201),
    });

    render(<MonitorCreatePage />);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Local health' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create monitor' }));

    await waitFor(() => expect(window.location.pathname).toBe('/monitors/monitor-9'));
    expect(JSON.parse(String(callsTo(fetchMock, 'POST', '/api/monitors')[0]?.[1]?.body))).toEqual({
      name: 'Local health',
      check: { url: 'http://127.0.0.1:8090/', expectedStatus: 200, deadlineMs: 10_000 },
      incidentPolicy: { openAfter: 2, recoverAfter: 2 },
    });
  });

  it('renders validation_failed errors next to the inputs they belong to', async () => {
    stubApi({
      ...INTERVALS_OK,
      'POST /api/monitors': () =>
        jsonResponse(
          {
            error: 'validation_failed',
            fields: {
              name: { code: 'required', message: 'name is required' },
              'check.url': {
                code: 'target_not_allowed',
                message: 'host:port 127.0.0.1:8080 is not in STATUSFORGE_ALLOWED_TARGETS',
              },
              'incidentPolicy.openAfter': {
                code: 'out_of_range',
                message: 'openAfter must be 1–5',
              },
              'incidentPolicy.recoverAfter': {
                code: 'out_of_range',
                message: 'recoverAfter must be 1–5',
              },
            },
          },
          400,
        ),
    });

    render(<MonitorCreatePage />);
    fireEvent.click(screen.getByRole('button', { name: 'Create monitor' }));

    const nameInput = screen.getByLabelText('Name');
    const urlInput = screen.getByLabelText('URL');
    const urlError = await screen.findByText(
      'host:port 127.0.0.1:8080 is not in STATUSFORGE_ALLOWED_TARGETS',
    );

    expect(screen.getByText('name is required')).toHaveAttribute('id', 'monitor-name-error');
    expect(nameInput).toHaveAttribute('aria-invalid', 'true');
    expect(nameInput.getAttribute('aria-describedby')).toContain('monitor-name-error');
    expect(urlError).toHaveAttribute('id', 'monitor-url-error');
    expect(urlInput.getAttribute('aria-describedby')).toContain('monitor-url-error');
    expect(await screen.findByText('openAfter must be 1–5')).toHaveAttribute(
      'id',
      'monitor-open-after-error',
    );
    expect(screen.getByLabelText('Open after failed checks')).toHaveAttribute(
      'aria-invalid',
      'true',
    );
    expect(screen.getByText('recoverAfter must be 1–5')).toHaveAttribute(
      'id',
      'monitor-recover-after-error',
    );
    expect(screen.getByLabelText('Resolve after healthy checks')).toHaveAttribute(
      'aria-invalid',
      'true',
    );
  });

  it('shows validation errors that belong to no input as a form message', async () => {
    stubApi({
      ...INTERVALS_OK,
      'POST /api/monitors': () =>
        jsonResponse(
          {
            error: 'validation_failed',
            fields: {
              body: { code: 'unknown_field', message: 'body is not a known field' },
            },
          },
          400,
        ),
    });

    render(<MonitorCreatePage />);
    fireEvent.click(screen.getByRole('button', { name: 'Create monitor' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('body is not a known field');
    expect(screen.getByLabelText('Name')).not.toHaveAttribute('aria-invalid');
  });

  it('keeps the entered values when the store is unavailable', async () => {
    stubApi({
      ...INTERVALS_OK,
      'POST /api/monitors': () => jsonResponse({ error: 'store_unavailable' }, 503),
    });

    render(<MonitorCreatePage />);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Local health' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create monitor' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The backend store is unavailable. Try again.',
    );
    expect(screen.getByLabelText('Name')).toHaveValue('Local health');
    expect(window.location.pathname).toBe('/');
  });
});
