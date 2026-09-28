import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { jsonResponse, monitorRecordFixture, observationFixture, stubApi } from '../test/fixtures';
import { ActivityPage } from './ActivityPage';

afterEach(() => vi.unstubAllGlobals());

it('merges heartbeat reports newest first and filters by heartbeat and evaluation', async () => {
  const first = monitorRecordFixture({
    id: 'first',
    name: 'First',
    kind: 'heartbeat',
    check: null,
    intervalSeconds: null,
    heartbeat: {
      intervalSeconds: 300,
      graceSeconds: 60,
      token: null,
      lastReportAt: null,
      ingestPath: '/ingest/heartbeats/first',
    },
    expectation: null,
  });
  const second = { ...first, id: 'second', name: 'Second' };
  const report = {
    runId: 'run',
    finishedAt: null,
    durationMs: 23,
    exitCode: 0,
    message: null,
    late: false,
  };
  const older = observationFixture({
    id: 'older',
    monitorId: 'first',
    kind: 'heartbeat_report',
    request: null,
    report,
    counted: false,
    notCountedReason: 'older_than_current',
    completedAt: '2026-09-27T10:00:00.000Z',
  });
  const paused = observationFixture({
    ...older,
    id: 'paused',
    report: { ...report, runId: 'paused' },
    notCountedReason: 'paused',
    completedAt: '2026-09-27T09:00:00.000Z',
  });
  const newest = observationFixture({
    id: 'newest',
    monitorId: 'second',
    kind: 'heartbeat_report',
    request: null,
    report: { ...report, runId: 'newest' },
    counted: true,
    completedAt: '2026-09-27T11:00:00.000Z',
  });
  stubApi({
    'GET /api/monitors': () => jsonResponse({ monitors: [first, second] }),
    'GET /api/monitors/first/observations?limit=50': () =>
      jsonResponse({ observations: [older, paused] }),
    'GET /api/monitors/second/observations?limit=50': () =>
      jsonResponse({ observations: [newest] }),
  });
  render(<ActivityPage />);
  const recent = await screen.findByText(/Run ID: newest/);
  const stale = screen.getByText(/Run ID: run(?!-)/);
  expect(recent.compareDocumentPosition(stale) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(stale).toHaveTextContent('Not counted — older than the newest report');
  expect(screen.getByText(/Run ID: paused/)).toHaveTextContent('Not counted — monitor was paused');
  fireEvent.change(screen.getByLabelText('Evaluation'), { target: { value: 'not-counted' } });
  expect(screen.queryByText(/Run ID: newest/)).not.toBeInTheDocument();
  expect(screen.getByText(/Run ID: run(?!-)/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Heartbeat'), { target: { value: 'second' } });
  expect(screen.getByText('No reports match these filters.')).toBeInTheDocument();
});
