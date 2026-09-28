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
    'GET /api/applications': () => jsonResponse({ applications: [] }),
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
  expect(screen.getByText('No activity matches these filters.')).toBeInTheDocument();
});
it('merges deployments by report time and filters both sources by application', async () => {
  const at = '2026-09-27T10:00:00.000Z';
  const app = {
    id: 'app-1',
    name: 'Payments',
    token: null,
    members: [],
    createdAt: at,
    updatedAt: at,
    archivedAt: null,
  };
  const other = { ...app, id: 'app-2', name: 'Search' };
  const monitor = monitorRecordFixture({
    id: 'heartbeat-1',
    applicationId: 'app-1',
    kind: 'heartbeat',
    check: null,
    intervalSeconds: null,
    heartbeat: {
      intervalSeconds: 300,
      graceSeconds: 60,
      token: null,
      lastReportAt: null,
      ingestPath: '/ingest/heartbeats/heartbeat-1',
    },
  });
  const report = observationFixture({
    id: 'report-1',
    kind: 'heartbeat_report',
    request: null,
    report: {
      runId: 'run-1',
      finishedAt: null,
      durationMs: null,
      exitCode: null,
      message: null,
      late: false,
    },
    completedAt: '2026-09-27T10:01:00.000Z',
  });
  const deployment = (id: string, applicationId: string, version: string, reportedAt: string) => ({
    id,
    applicationId,
    version,
    description: null,
    link: null,
    deployedAt: null,
    deploymentId: null,
    source: 'ingest',
    reportedAt,
  });
  stubApi({
    'GET /api/monitors': () => jsonResponse({ monitors: [monitor] }),
    'GET /api/applications': () => jsonResponse({ applications: [app, other] }),
    'GET /api/monitors/heartbeat-1/observations?limit=50': () =>
      jsonResponse({ observations: [report] }),
    'GET /api/applications/app-1/deployments?limit=50': () =>
      jsonResponse({ deployments: [deployment('d1', 'app-1', '2.0', '2026-09-27T10:02:00.000Z')] }),
    'GET /api/applications/app-2/deployments?limit=50': () =>
      jsonResponse({ deployments: [deployment('d2', 'app-2', '3.0', at)] }),
  });
  render(<ActivityPage />);
  const latest = await screen.findByText(/Deployment 2.0/);
  const reportRow = screen.getByText(/Run ID: run-1/);
  const oldest = screen.getByText(/Deployment 3.0/);
  expect(latest.compareDocumentPosition(reportRow) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(reportRow.compareDocumentPosition(oldest) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  fireEvent.change(screen.getByLabelText('Application'), { target: { value: 'app-1' } });
  expect(screen.queryByText(/Deployment 3.0/)).not.toBeInTheDocument();
  expect(screen.getByText(/Run ID: run-1/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Heartbeat'), { target: { value: 'heartbeat-1' } });
  expect(screen.queryByText(/Deployment 2.0/)).not.toBeInTheDocument();
});
