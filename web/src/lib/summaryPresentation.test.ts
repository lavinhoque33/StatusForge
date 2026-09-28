import { describe, expect, it } from 'vitest';
import { summaryFixture } from '../test/summaryFixture';
import {
  chartRows,
  coverageSentence,
  latencyPlotRows,
  latencyAxisMaximum,
  latencySentence,
  summaryNotes,
  shadedSpans,
} from './summaryPresentation';

describe('HTTP summary presentation', () => {
  it('names denominator, not observed slots, counted outcomes, and exclusions', () => {
    expect(coverageSentence(summaryFixture)).toContain(
      'Last 24 hours: 280 of 288 expected checks recorded (97 %); 8 not observed. Of 272 counted: 270 healthy, 2 failing',
    );
    expect(coverageSentence(summaryFixture)).toContain('Paused 30 min; maintenance 6 checks');
    expect(latencySentence(summaryFixture)).toContain(
      'median 12 ms, p95 40 ms, max 1.5 s over 272 responses; 0 without response',
    );
  });
  it('labels partial windows, pending slots, and unknown pause history without making missing results healthy', () => {
    const partial = {
      ...summaryFixture,
      window: '7d' as const,
      truncated: true,
      coveredFrom: '2026-09-27T06:00:00.000Z',
      pendingSince: '2026-09-27T20:00:00.000Z',
      lifecycleHistoryFrom: null,
    };
    expect(coverageSentence(partial)).toContain('Last 7 days');
    expect(summaryNotes(partial, '2026-01-01T00:00:00.000Z')).toEqual([
      expect.stringContaining('Partial window — covers from'),
      expect.stringContaining('Not yet accounted for since'),
      expect.stringContaining('Pause periods before'),
    ]);
    const historyFrom = '2026-09-27T09:00:00.000Z';
    const withHistory = { ...summaryFixture, lifecycleHistoryFrom: historyFrom };
    expect(summaryNotes(withHistory, historyFrom)).not.toContainEqual(
      expect.stringContaining('Pause periods before'),
    );
    expect(summaryNotes(withHistory, '2026-01-01T00:00:00.000Z')).toContainEqual(
      expect.stringContaining('Pause periods before'),
    );
    expect(
      latencySentence({
        ...partial,
        latency: { ...partial.latency, samples: 4, medianMs: null, p95Ms: null, noResponse: 2 },
      }),
    ).toContain('too few samples (4 responses; 2 without response');
  });
  it('transforms exact bucket totals and retains missing latency as null', () => {
    const rows = chartRows(summaryFixture);
    expect(rows.map((row) => row.expected)).toEqual([12, 12]);
    expect(rows.map((row) => row.notObserved)).toEqual([0, 12]);
    expect(rows.map((row) => row.medianMs)).toEqual([11, null]);
    expect(rows.map((row) => row.maxMs)).toEqual([16, null]);
  });
  it('breaks the latency line during a receive outage without altering table totals', () => {
    const summary = {
      ...summaryFixture,
      outages: [{ from: '2026-09-27T00:35:00.000Z', to: '2026-09-27T01:25:00.000Z' }],
    };
    const plotted = latencyPlotRows(summary);
    expect(plotted.map((row) => row.medianMs)).toEqual([11, null, null]);
    expect(chartRows(summary).map((row) => row.samples)).toEqual([12, 0]);
  });
  it('keeps the deadline in the latency scale even when responses are faster', () => {
    expect(latencyAxisMaximum(summaryFixture, 5000)).toBe(5500);
    const slow = {
      ...summaryFixture,
      buckets: [
        {
          ...summaryFixture.buckets[0],
          latency: { ...summaryFixture.buckets[0].latency, maxMs: 8000 },
        },
      ],
    };
    expect(latencyAxisMaximum(slow, 5000)).toBe(8800);
  });
  it('merges touching outages and pauses into one labelled shaded span', () => {
    const summary = {
      ...summaryFixture,
      outages: [
        { from: '2026-09-27T00:05:00.000Z', to: '2026-09-27T00:20:00.000Z' },
        { from: '2026-09-27T00:20:00.000Z', to: '2026-09-27T00:35:00.000Z' },
      ],
      buckets: [{ ...summaryFixture.buckets[0], pausedSeconds: 300 }],
    };
    expect(shadedSpans(summary, chartRows(summary), false)).toEqual([
      {
        from: Date.parse('2026-09-27T00:05:00.000Z'),
        to: Date.parse('2026-09-27T00:35:00.000Z'),
        outage: true,
        paused: false,
      },
    ]);
    expect(shadedSpans(summary, chartRows(summary), true)).toEqual([
      {
        from: Date.parse(summary.from),
        to: Date.parse('2026-09-27T01:00:00.000Z'),
        outage: true,
        paused: true,
      },
    ]);
  });
});
