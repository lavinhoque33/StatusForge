import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { getMonitorSummary } from '../api/dailyUse';
import { summaryFixture } from '../test/summaryFixture';
import { HttpSummaryPanel } from './HttpSummaryPanel';

vi.mock('../api/dailyUse', () => ({ getMonitorSummary: vi.fn() }));
vi.mock('./SummaryCharts', () => ({ default: () => <div>Charts loaded</div> }));
afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});
it('refreshes HTTP summary on toggle and at 60 s, not at 15 s', async () => {
  vi.mocked(getMonitorSummary).mockImplementation(async (_id, window) => ({
    ...summaryFixture,
    window,
  }));
  vi.useFakeTimers();
  render(
    <HttpSummaryPanel
      monitorId="monitor-1"
      createdAt="2026-01-01T00:00:00.000Z"
      deadlineMs={5000}
    />,
  );
  await act(async () => {
    await Promise.resolve();
  });
  expect(screen.getByText(/280 of 288 expected checks recorded/)).toBeInTheDocument();
  expect(getMonitorSummary).toHaveBeenCalledTimes(1);
  await act(async () => {
    vi.advanceTimersByTime(15_000);
  });
  expect(getMonitorSummary).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('radio', { name: '7 d' }));
  await act(async () => {
    await Promise.resolve();
  });
  expect(getMonitorSummary).toHaveBeenLastCalledWith('monitor-1', '7d', expect.any(AbortSignal));
  await act(async () => {
    vi.advanceTimersByTime(60_000);
  });
  expect(getMonitorSummary).toHaveBeenCalledTimes(3);
});

it('labels retained 24 h data when the 7 d request fails', async () => {
  vi.mocked(getMonitorSummary)
    .mockResolvedValueOnce(summaryFixture)
    .mockRejectedValueOnce(new Error('Temporary network error'));
  render(
    <HttpSummaryPanel
      monitorId="monitor-1"
      createdAt="2026-01-01T00:00:00.000Z"
      deadlineMs={5000}
    />,
  );
  expect(await screen.findByText(/Last 24 hours: 280 of 288/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('radio', { name: '7 d' }));
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Showing last available summary for 24h.',
  );
  expect(screen.getByText(/Last 24 hours: 280 of 288/)).toBeInTheDocument();
});

it('mirrors the sentence counts in the stat strip and shares the latency sample gate', async () => {
  vi.mocked(getMonitorSummary).mockResolvedValueOnce({
    ...summaryFixture,
    coveredFrom: '2026-09-27T06:00:00.000Z',
    latency: { ...summaryFixture.latency, samples: 4 },
  });
  render(
    <HttpSummaryPanel
      monitorId="monitor-1"
      createdAt="2026-01-01T00:00:00.000Z"
      deadlineMs={5000}
    />,
  );
  expect(await screen.findByText(/too few samples \(4 responses;/)).toBeInTheDocument();
  const strip = screen.getByText('Partial window').closest('.summary-stats')!;
  expect(strip).toHaveTextContent('280 of 288 expected checks recorded (97 %)');
  expect(strip).toHaveTextContent('270 healthy, 2 failing, 0 checker problem');
  expect(strip).toHaveTextContent('too few samples 4 responses');
  expect(strip).not.toHaveTextContent('median');
});
