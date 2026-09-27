import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { jsonResponse, monitorFixture, stubApi } from '../test/fixtures';
import { MonitorCreatePage } from './MonitorCreatePage';

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.pushState(null, '', '/');
});

describe('MonitorCreatePage', () => {
  it('prefills the sample target and the contract defaults', () => {
    stubApi({});

    render(<MonitorCreatePage />);

    expect(screen.getByLabelText('Name')).toHaveValue('');
    expect(screen.getByLabelText('URL')).toHaveValue('http://127.0.0.1:8090/');
    expect(screen.getByLabelText('Expected status')).toHaveValue(200);
    expect(screen.getByLabelText('Deadline (seconds)')).toHaveValue(10);
  });

  it('sends seconds as deadlineMs and opens the created monitor', async () => {
    const fetchMock = stubApi({
      'POST /api/monitors': () => jsonResponse(monitorFixture({ id: 'monitor-9' }), 201),
    });

    render(<MonitorCreatePage />);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Local health' } });
    fireEvent.change(screen.getByLabelText('Deadline (seconds)'), { target: { value: '1.5' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create monitor' }));

    await waitFor(() => expect(window.location.pathname).toBe('/monitors/monitor-9'));
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      name: 'Local health',
      check: { url: 'http://127.0.0.1:8090/', expectedStatus: 200, deadlineMs: 1500 },
    });
  });

  it('renders validation_failed errors next to the inputs they belong to', async () => {
    stubApi({
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
  });

  it('shows validation errors that belong to no input as a form message', async () => {
    stubApi({
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
    stubApi({ 'POST /api/monitors': () => jsonResponse({ error: 'store_unavailable' }, 503) });

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
