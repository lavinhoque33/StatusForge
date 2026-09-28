import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { parseMonitorSummary, type HeartbeatSummary } from '../api/dailyUse';
import SummaryCharts from '../components/SummaryCharts';
import { summaryFixture } from '../test/summaryFixture';
import { coverageSentence, heartbeatChartRows, summaryNotes } from './summaryPresentation';

vi.stubGlobal(
  'ResizeObserver',
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
);
const heartbeat: HeartbeatSummary = {
  ...summaryFixture,
  kind: 'heartbeat',
  latency: null,
  coverage: {
    expected: 12,
    recorded: 10,
    notObserved: 2,
    maintenance: 1,
    notCounted: 1,
    notCountedReasons: { paused: 1 },
    pausedSeconds: 60,
    outcomes: { onTime: 5, late: 2, missed: 2, failureReports: 1 },
  },
  buckets: [
    {
      from: summaryFixture.from,
      to: summaryFixture.to,
      expected: 12,
      recorded: 10,
      notObserved: 2,
      maintenance: 1,
      notCounted: 1,
      pausedSeconds: 60,
      onTime: 5,
      late: 2,
      missed: 2,
      failureReports: 1,
    },
  ],
};
it('keeps heartbeat report failures a subset, and never presents missing deadlines as success', () => {
  const parsed = parseMonitorSummary(heartbeat);
  expect(parsed.kind).toBe('heartbeat');
  expect(coverageSentence(parsed)).toMatch(
    /10 of 12 expected heartbeat deadlines recorded.*2 not observed.*5 on time, 2 late, 2 missed; 1 failure reports/,
  );
  expect(heartbeatChartRows(heartbeat)[0]).toMatchObject({
    onTime: 5,
    late: 2,
    missed: 2,
    failureReports: 1,
    notObserved: 2,
  });
  expect(summaryNotes(heartbeat, heartbeat.lifecycleHistoryFrom!)).toEqual([]);
  render(<SummaryCharts summary={heartbeat} />);
  expect(screen.getAllByRole('button', { name: 'Show as table' })).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: 'Show as table' }));
  const table = screen.getByRole('table', { name: /Status history by bucket/ });
  expect(table).toHaveTextContent('Failure reports (subset)');
  expect(table).toHaveTextContent('Not observed');
  expect(table).toHaveTextContent('Missed');
  expect(screen.queryByText(/Response time by bucket/)).not.toBeInTheDocument();
});
it('rejects malformed heartbeat bucket and non-null latency', () => {
  expect(() => parseMonitorSummary({ ...heartbeat, latency: {} })).toThrow();
  expect(() =>
    parseMonitorSummary({ ...heartbeat, buckets: [{ ...heartbeat.buckets[0], missed: -1 }] }),
  ).toThrow();
});
