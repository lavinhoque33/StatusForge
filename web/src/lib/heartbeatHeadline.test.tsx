import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { heartbeatDeadline, heartbeatHeadline, heartbeatState } from './heartbeatHeadline';
import { mergeActivity } from './activity';
import { TimelineTable } from '../components/TimelineTable';
import { gapFixture, monitorRecordFixture, observationFixture } from '../test/fixtures';
import { formatLocalWithOffset } from './time';

const now = Date.parse('2026-09-27T12:00:00.000Z');
const due = new Date(now + 15_000).toISOString();
const missing = new Date(now + 20_000).toISOString();
const report = observationFixture({
  kind: 'heartbeat_report',
  request: null,
  observedStatus: undefined,
  completedAt: new Date(now).toISOString(),
  report: {
    runId: 'run-1',
    finishedAt: null,
    durationMs: 42,
    exitCode: 0,
    message: '<img src=x onerror=alert(1)>',
    late: false,
  },
});
const monitor = monitorRecordFixture(
  {
    kind: 'heartbeat',
    intervalSeconds: null,
    check: null,
    heartbeat: {
      intervalSeconds: 15,
      graceSeconds: 5,
      token: null,
      lastReportAt: new Date(now - 30_000).toISOString(),
      ingestPath: '/ingest/heartbeats/monitor-1',
    },
    expectation: {
      dueAt: due,
      lateAt: due,
      missingAt: missing,
      staleAt: new Date(now + 55_000).toISOString(),
    },
  },
  { state: 'healthy', reason: null, observation: report, freshUntil: missing },
);

describe('heartbeat headlines and exact transitions', () => {
  it('expresses on-time, late, stale, waiting, missing and reported failure without green for late', () => {
    expect(heartbeatHeadline(monitor, now)).toBe(
      `On time — last report 30 s ago; next due by ${formatLocalWithOffset(new Date(due))}`,
    );
    const justReported = {
      ...monitor,
      heartbeat: { ...monitor.heartbeat!, lastReportAt: new Date(now).toISOString() },
    };
    expect(heartbeatHeadline(justReported, now)).toBe(
      `On time — last report just now; next due by ${formatLocalWithOffset(new Date(due))}`,
    );
    expect(heartbeatDeadline(monitor, now)).toBe(now + 15_000);
    expect(heartbeatState(monitor, now + 14_999)).toBe('healthy');
    expect(heartbeatState(monitor, now + 15_000)).toBe('late');
    expect(heartbeatHeadline(monitor, now + 15_000)).toContain('Late — due at');
    expect(heartbeatDeadline(monitor, now + 15_000)).toBe(now + 55_000);
    expect(heartbeatState(monitor, now + 55_000)).toBe('stale');
    expect(heartbeatHeadline(monitor, now + 55_000)).toBe('Stale — deadlines not being checked');
    const waiting = {
      ...monitor,
      status: {
        ...monitor.status,
        state: 'unknown' as const,
        reason: 'waiting_for_first_report' as const,
        observation: null,
      },
    };
    expect(heartbeatHeadline(waiting, now)).toContain('Waiting for first report — due by');
    expect(heartbeatHeadline(waiting, now + 15_000)).toContain('Late — due at');
    const missingMonitor = {
      ...monitor,
      status: {
        ...monitor.status,
        state: 'failing' as const,
        reason: 'missing' as const,
        observation: observationFixture({ kind: 'heartbeat_missed', request: null, dueAt: due }),
      },
    };
    expect(heartbeatHeadline(missingMonitor, now)).toContain('Missing — no report since');
    const neverReported = {
      ...missingMonitor,
      heartbeat: { ...monitor.heartbeat!, lastReportAt: null },
    };
    expect(heartbeatHeadline(neverReported, now)).toContain('Missing — no report received yet');
    const failed = {
      ...monitor,
      status: {
        ...monitor.status,
        state: 'failing' as const,
        reason: 'reported_failure' as const,
        observation: { ...report, report: { ...report.report!, exitCode: 7 } },
      },
    };
    expect(heartbeatHeadline(failed, now)).toBe('Failing — job reported failure just now (exit 7)');
    const failedEarlier = {
      ...failed,
      status: {
        ...failed.status,
        observation: {
          ...report,
          completedAt: new Date(now - 9_000).toISOString(),
          report: { ...report.report!, exitCode: 7 },
        },
      },
    };
    expect(heartbeatHeadline(failedEarlier, now)).toBe(
      'Failing — job reported failure 9 s ago (exit 7)',
    );
  });
});

describe('heartbeat history and activity', () => {
  it('renders report fields as text, misses and not-observed gaps', () => {
    const missed = observationFixture({
      id: 'miss',
      kind: 'heartbeat_missed',
      request: null,
      dueAt: due,
      reason: 'missing',
    });
    const { container } = render(
      <TimelineTable
        observations={[report, missed]}
        gaps={[gapFixture({ reason: 'not_observed' })]}
      />,
    );
    expect(screen.getByText('Received')).toBeInTheDocument();
    expect(screen.getByText('Missing')).toBeInTheDocument();
    expect(screen.getByText('Not observed — StatusForge was not receiving')).toBeInTheDocument();
    expect(screen.getByText(/Run ID: run-1/)).toBeInTheDocument();
    expect(screen.getByText(/Exit code: 0/)).toBeInTheDocument();
    expect(screen.getByText(/<img src=x onerror=alert\(1\)>/)).toBeInTheDocument();
    expect(container.querySelector('img')).toBeNull();
  });
  it('merges across heartbeats by receipt time, filters not counted without changing order', () => {
    const other = { ...monitor, id: 'other', name: 'Other' };
    const older = {
      ...report,
      id: 'older',
      monitorId: 'other',
      completedAt: new Date(now - 1000).toISOString(),
      counted: false,
    };
    const newest = {
      ...report,
      id: 'newer',
      completedAt: new Date(now + 1000).toISOString(),
      counted: true,
    };
    const merged = mergeActivity([monitor, other], [[newest], [older]]);
    expect(merged.map(({ observation }) => observation.id)).toEqual(['newer', 'older']);
    expect(
      merged
        .filter(({ observation }) => !observation.counted)
        .map(({ observation }) => observation.id),
    ).toEqual(['older']);
  });
});
