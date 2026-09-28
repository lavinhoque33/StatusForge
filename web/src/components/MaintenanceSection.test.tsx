import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { jsonResponse, stubApi, callsTo } from '../test/fixtures';
import type { Window } from '../api/maintenance';
import { MaintenanceSection } from './MaintenanceSection';

const base: Window = {
  id: 'w1',
  monitorId: 'monitor-1',
  startAt: '2026-09-27T10:00:00.000Z',
  endAt: '2026-09-27T12:00:00.000Z',
  note: 'Deploy',
  createdAt: '2026-09-27T09:00:00.000Z',
  cancelledAt: null,
  state: 'scheduled',
};
const route = 'POST /api/monitors/monitor-1/maintenance/w1/cancel';
afterEach(() => vi.unstubAllGlobals());

describe('MaintenanceSection', () => {
  it.each([
    { state: 'scheduled', action: 'Cancel' },
    { state: 'active', action: 'End now' },
  ] as const)('confirms $action and moves focus after closing', async ({ state, action }) => {
    const fetchMock = stubApi({
      [route]: () =>
        jsonResponse({ ...base, state: 'cancelled', cancelledAt: '2026-09-27T11:00:00.000Z' }),
    });
    const refresh = vi.fn();
    render(
      <MaintenanceSection
        monitorId="monitor-1"
        archived={false}
        windows={[{ ...base, state }]}
        refresh={refresh}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: action }));
    expect(screen.getByRole('button', { name: `Confirm ${action.toLowerCase()}` })).toHaveFocus();
    expect(
      callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/maintenance/w1/cancel'),
    ).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Keep window' }));
    await waitFor(() => expect(screen.getByRole('button', { name: action })).toHaveFocus());
    fireEvent.click(screen.getByRole('button', { name: action }));
    fireEvent.click(screen.getByRole('button', { name: `Confirm ${action.toLowerCase()}` }));
    await waitFor(() => expect(refresh).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Maintenance' })).toHaveFocus());
    expect(
      callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/maintenance/w1/cancel'),
    ).toHaveLength(1);
  });

  it('maps overlap and limit errors and reloads after window_closed', async () => {
    const fetchMock = stubApi({
      'POST /api/monitors/monitor-1/maintenance': () =>
        jsonResponse(
          {
            error: 'validation_failed',
            fields: {
              startAt: { code: 'overlaps' },
              endAt: { code: 'out_of_range' },
              windows: { code: 'too_many_windows' },
            },
          },
          400,
        ),
      [route]: () => jsonResponse({ error: 'window_closed' }, 409),
    });
    const refresh = vi.fn();
    render(
      <MaintenanceSection
        monitorId="monitor-1"
        archived={false}
        windows={[base]}
        refresh={refresh}
      />,
    );
    fireEvent.change(screen.getByLabelText('Start'), { target: { value: '2026-09-27T10:00' } });
    fireEvent.change(screen.getByLabelText('End'), { target: { value: '2026-09-27T11:00' } });
    fireEvent.click(screen.getByRole('button', { name: 'Schedule maintenance' }));
    expect(
      await screen.findByText('This window overlaps another active or scheduled window.'),
    ).toBeInTheDocument();
    expect(
      screen.getByText('At most 10 active or scheduled windows are allowed.'),
    ).toBeInTheDocument();
    expect(screen.getByText('This value is out of range.')).toBeInTheDocument();
    expect(callsTo(fetchMock, 'POST', '/api/monitors/monitor-1/maintenance')).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm cancel' }));
    expect(await screen.findByText(/Windows reloaded/)).toBeInTheDocument();
    expect(refresh).toHaveBeenCalledOnce();
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Maintenance' })).toHaveFocus());
  });
});
