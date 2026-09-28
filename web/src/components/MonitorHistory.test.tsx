import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { getGapPage, getObservationPage } from '../api/monitors';
import { gapFixture, observationFixture } from '../test/fixtures';
import { MonitorHistory } from './MonitorHistory';

vi.mock('../api/monitors', async (original) => ({
  ...(await original()),
  getObservationPage: vi.fn(),
  getGapPage: vi.fn(),
}));
afterEach(() => {
  vi.clearAllMocks();
  vi.useRealTimers();
});
const observed = (id: string, minute: number) =>
  observationFixture({
    id,
    startedAt: `2026-09-27T10:${minute.toString().padStart(2, '0')}:00.000Z`,
    completedAt: `2026-09-27T10:${minute.toString().padStart(2, '0')}:00.000Z`,
  });
it('merges an older page with a changed head without duplicate records, preserving filters and searchedThrough', async () => {
  const newest = observed('new', 40);
  const first = observed('first', 30);
  const older = observed('older', 20);
  vi.mocked(getGapPage).mockResolvedValue({ items: [], nextCursor: null, searchedThrough: null });
  vi.mocked(getObservationPage).mockImplementation(async (_id, _limit, _filters, before) =>
    before
      ? { items: [first, older], nextCursor: null, searchedThrough: older.completedAt }
      : { items: [first], nextCursor: 'cursor', searchedThrough: first.completedAt },
  );
  render(
    <MonitorHistory monitorId="monitor-1" monitorKind="http" windows={[]} configVersion={1} />,
  );
  expect(await screen.findByText(/Searched back to/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'failing' } });
  await waitFor(() =>
    expect(getObservationPage).toHaveBeenLastCalledWith(
      'monitor-1',
      50,
      expect.objectContaining({ outcome: 'failing' }),
      undefined,
      expect.any(AbortSignal),
    ),
  );
  fireEvent.click(screen.getByRole('button', { name: 'Load older observations' }));
  await waitFor(() =>
    expect(screen.getByRole('table').querySelectorAll('tbody tr')).toHaveLength(2),
  );
  vi.mocked(getObservationPage).mockImplementation(async (_id, _limit, filters, before) => {
    expect(filters.outcome).toBe('failing');
    expect(before).toBeUndefined();
    return { items: [newest, first], nextCursor: 'new-cursor', searchedThrough: first.completedAt };
  });
  // Becoming visible invokes the same head-only refresh as the 15 s timer.
  await act(async () => {
    fireEvent(document, new Event('visibilitychange'));
    await Promise.resolve();
  });
  await waitFor(() =>
    expect(screen.getByRole('table').querySelectorAll('tbody tr')).toHaveLength(3),
  );
  expect(screen.getByLabelText('Outcome')).toHaveValue('failing');
  expect(screen.getByText(/Searched back to/)).toBeInTheDocument();
});

it('preserves a mid-history cursor across a poll and never re-arms exhausted observations or gaps', async () => {
  let heads = 0;
  vi.mocked(getObservationPage).mockImplementation(async (_id, _limit, _filters, before) => {
    if (before === 'head-cursor')
      return { items: [observed('older', 20)], nextCursor: 'tail-cursor', searchedThrough: null };
    if (before === 'tail-cursor')
      return { items: [observed('oldest', 10)], nextCursor: null, searchedThrough: null };
    heads += 1;
    return {
      items: [observed(`head-${heads}`, 40 + heads)],
      nextCursor: `head-${heads}-cursor`,
      searchedThrough: null,
    };
  });
  // The first head is intentionally a distinct cursor from later polls.
  vi.mocked(getObservationPage).mockResolvedValueOnce({
    items: [observed('head-0', 40)],
    nextCursor: 'head-cursor',
    searchedThrough: null,
  });
  vi.mocked(getGapPage).mockImplementation(async (_id, _limit, before) =>
    before
      ? { items: [gapFixture({ id: 'older-gap' })], nextCursor: null, searchedThrough: null }
      : { items: [], nextCursor: 'gap-cursor', searchedThrough: null },
  );
  render(
    <MonitorHistory monitorId="monitor-1" monitorKind="http" windows={[]} configVersion={1} />,
  );
  const loadObservations = await screen.findByRole('button', { name: 'Load older observations' });
  loadObservations.focus();
  fireEvent.click(loadObservations);
  await waitFor(() =>
    expect(screen.getByRole('table').querySelectorAll('tbody tr')).toHaveLength(2),
  );
  expect(loadObservations).toHaveFocus();
  await act(async () => {
    fireEvent(document, new Event('visibilitychange'));
    await Promise.resolve();
  });
  await waitFor(() =>
    expect(screen.getByRole('table').querySelectorAll('tbody tr')).toHaveLength(3),
  );
  fireEvent.click(loadObservations);
  await waitFor(() =>
    expect(vi.mocked(getObservationPage).mock.calls.at(-1)?.[3]).toBe('tail-cursor'),
  );
  expect(await screen.findByText(/No older checks.*90 days/)).toHaveFocus();
  fireEvent.click(screen.getByRole('button', { name: 'Load older gaps' }));
  await waitFor(() => expect(screen.getByText(/No older gaps.*90 days/)).toHaveFocus());
  await act(async () => {
    fireEvent(document, new Event('visibilitychange'));
    await Promise.resolve();
  });
  expect(screen.queryByRole('button', { name: 'Load older observations' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Load older gaps' })).not.toBeInTheDocument();
});

it('explains an observation filter with no matches even when gap rows remain', async () => {
  vi.mocked(getObservationPage).mockResolvedValue({
    items: [],
    nextCursor: null,
    searchedThrough: null,
  });
  vi.mocked(getGapPage).mockResolvedValue({
    items: [gapFixture()],
    nextCursor: null,
    searchedThrough: null,
  });
  render(
    <MonitorHistory monitorId="monitor-1" monitorKind="http" windows={[]} configVersion={1} />,
  );
  await screen.findByRole('table');
  fireEvent.change(screen.getByLabelText('Counted'), { target: { value: 'false' } });
  expect(
    await screen.findByText('No observations match these filters in the searched range.'),
  ).toBeInTheDocument();
  expect(screen.getByRole('table')).toHaveTextContent('Gap');
});
