import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { callsTo, jsonResponse, stubApi } from '../test/fixtures';
import { PermanentDeletion } from './PermanentDeletion';

const at = '2026-09-28T10:00:00.000Z';
const waiting = {
  state: 'waiting_for_notifications' as const,
  requestedAt: at,
  updatedAt: at,
  removedItems: 0,
};
const deleting = { ...waiting, state: 'deleting' as const, removedItems: 25 };
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
it('requires exact name, returns focus on cancel, shows mismatch and reports deletion through completion', async () => {
  const onComplete = vi.fn();
  const onStatus = vi.fn();
  let attempted = 0;
  let progress = 0;
  const fetchMock = stubApi({
    'POST /api/monitors/m1/deletion': () =>
      ++attempted === 1
        ? jsonResponse(
            {
              error: 'validation_failed',
              fields: { confirmName: { code: 'mismatch', message: '' } },
            },
            400,
          )
        : jsonResponse(waiting, 202),
    'GET /api/monitors/m1/deletion': () =>
      ++progress === 1 ? jsonResponse(deleting) : jsonResponse({ error: 'monitor_not_found' }, 404),
  });
  render(
    <PermanentDeletion
      resource="monitors"
      id="m1"
      name="Critical HTTP"
      deletion={null}
      onStatus={onStatus}
      onComplete={onComplete}
    />,
  );
  const trigger = screen.getByRole('button', { name: 'Delete permanently' });
  fireEvent.click(trigger);
  const input = screen.getByRole('textbox', { name: 'Type Critical HTTP to delete permanently' });
  expect(input).toHaveFocus();
  expect(screen.getByRole('button', { name: 'Confirm permanent deletion' })).toBeDisabled();
  fireEvent.change(input, { target: { value: 'critical HTTP' } });
  expect(screen.getByRole('button', { name: 'Confirm permanent deletion' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Delete permanently' })).toHaveFocus(),
  );
  fireEvent.click(screen.getByRole('button', { name: 'Delete permanently' }));
  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Critical HTTP' } });
  fireEvent.click(screen.getByRole('button', { name: 'Confirm permanent deletion' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('exact current name');
  expect(callsTo(fetchMock, 'POST', '/api/monitors/m1/deletion')).toHaveLength(1);
  vi.useFakeTimers();
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Confirm permanent deletion' }));
    await Promise.resolve();
  });
  expect(screen.getByText('Waiting for pending notifications to finish')).toBeInTheDocument();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
  expect(screen.getByText('Deleting — 25 records removed')).toBeInTheDocument();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
  expect(onComplete).toHaveBeenCalledOnce();
  expect(onStatus).toHaveBeenCalledWith(deleting);
});

it.each([
  ['not_archived', 409, 'Archive this item before deleting it permanently'],
  ['deleting', 409, 'This item is being deleted'],
  ['store_unavailable', 503, 'backend store is unavailable'],
])(
  'explains %s deletion rejection and keeps the confirmation available',
  async (code, status, expected) => {
    stubApi({
      'POST /api/applications/app-1/deletion': () => jsonResponse({ error: code }, status),
    });
    render(
      <PermanentDeletion
        resource="applications"
        id="app-1"
        name="Payments"
        deletion={null}
        onStatus={vi.fn()}
        onComplete={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Delete permanently' }));
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Payments' } });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm permanent deletion' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(expected);
    expect(screen.getByRole('button', { name: 'Confirm permanent deletion' })).toBeEnabled();
  },
);
