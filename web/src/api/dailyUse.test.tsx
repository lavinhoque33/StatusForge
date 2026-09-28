import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ApiInvalidResponseError } from './http';
import { parseHttpSummary, parseOverview } from './dailyUse';
import { summaryFixture } from '../test/summaryFixture';
import SummaryCharts from '../components/SummaryCharts';

const overview = {
  evaluatedAt: '2026-09-28T12:00:00.000Z',
  receiveOutages: [],
  openIncidents: [],
  failingWithoutIncident: [],
  coverageProblems: [],
  notifications: { count: 0, items: [] },
  recentRecoveries: [],
  counts: {
    active: 0,
    paused: 0,
    archived: 0,
    byState: { healthy: 0, late: 0, failing: 0, checker_problem: 0, stale: 0, unknown: 0 },
  },
  limits: { openIncidents: 50, recentRecoveries: 20, notifications: 10, recoveryWindowHours: 24 },
};
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
);
describe('daily-use API boundaries', () => {
  it('accepts the documented summary and overview and rejects missing or malformed counts', () => {
    expect(parseHttpSummary(summaryFixture).coverage.expected).toBe(288);
    expect(parseOverview(overview).counts.byState.healthy).toBe(0);
    expect(() =>
      parseHttpSummary({ ...summaryFixture, latency: { ...summaryFixture.latency, samples: -1 } }),
    ).toThrow(ApiInvalidResponseError);
    expect(() =>
      parseOverview({
        ...overview,
        counts: { ...overview.counts, byState: { ...overview.counts.byState, unknown: '0' } },
      }),
    ).toThrow(ApiInvalidResponseError);
  });
  it('renders exactly the transformed bucket values in chart alternatives, preserving empty latency', () => {
    render(<SummaryCharts summary={summaryFixture} deadlineMs={5000} />);
    fireEvent.click(screen.getAllByRole('button', { name: 'Show as table' })[0]);
    const status = screen.getByRole('table', { name: /Status history by bucket/ });
    expect(status.querySelector('caption')).toHaveTextContent(
      'Status history by bucket — 24h, 1 h buckets. Denominator: expected scheduled checks; paused time excluded.',
    );
    expect(status).toHaveTextContent('Not observed');
    expect(status).toHaveTextContent('12');
    fireEvent.click(screen.getByRole('button', { name: 'Show as table' }));
    const latency = screen.getByRole('table', { name: /Response time by bucket/ });
    expect(latency.querySelector('caption')).toHaveTextContent(
      'Response time by bucket — 24h, 1 h buckets. Denominator: responses within the check deadline (5000 ms); no-response and checker problems excluded.',
    );
    expect(latency).toHaveTextContent('No median');
    expect(latency).toHaveTextContent('No response');
  });
});
