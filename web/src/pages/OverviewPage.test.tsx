import { render, screen, act } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Overview } from '../api/dailyUse';
import { getOverview } from '../api/dailyUse';
import { listApplications } from '../api/applications';
import type { Incident } from '../api/incidents';
import { monitorStatusFixture } from '../test/fixtures';
import { groupByApplication } from '../lib/applicationGroups';
import { OverviewPage } from './OverviewPage';

vi.mock('../api/dailyUse', () => ({ getOverview: vi.fn() }));
vi.mock('../api/applications', () => ({ listApplications: vi.fn() }));
vi.mocked(listApplications).mockResolvedValue([
  {
    id: 'billing',
    name: 'Billing',
    token: null,
    members: [],
    createdAt: '2026-09-28T12:00:00.000Z',
    updatedAt: '2026-09-28T12:00:00.000Z',
    archivedAt: null,
    deletion: null,
  },
]);
const data: Overview = {
  evaluatedAt: '2026-09-28T12:00:00.000Z',
  receiveOutages: [],
  scheduler: { state: 'ok', windowMinutes: 5, dueChecks: 120, missedChecks: 0, workers: 4 },
  openIncidents: [],
  failingWithoutIncident: [],
  coverageProblems: [],
  notifications: { count: 0, items: [] },
  recentRecoveries: [],
  counts: {
    active: 2,
    paused: 1,
    archived: 0,
    byState: { healthy: 2, late: 0, failing: 0, checker_problem: 0, stale: 0, unknown: 0 },
  },
  limits: { openIncidents: 50, recentRecoveries: 20, notifications: 10, recoveryWindowHours: 24 },
};
const monitor = {
  id: 'm1',
  name: 'Fixture',
  kind: 'http' as const,
  applicationId: null,
  applicationName: null,
};
const incident: Incident = {
  id: 'i1',
  monitorId: 'm1',
  applicationId: null,
  monitorName: 'Fixture',
  state: 'resolved',
  resolution: 'recovered',
  openedAt: '2026-09-28T10:00:00.000Z',
  resolvedAt: '2026-09-28T11:00:00.000Z',
  openingEvidence: [],
  recoveryEvidence: [],
  failureCount: 1,
  firstFailureAt: '2026-09-28T10:00:00.000Z',
  lastFailure: {
    kind: 'http_check',
    observationId: 'o1',
    startedAt: '2026-09-28T10:00:00.000Z',
    initiatedBy: 'scheduled',
    outcome: 'failing',
    reason: 'wrong_status',
    observedStatus: 503,
    configVersion: 1,
  },
  checkerProblemCount: 0,
  lastCheckerProblem: null,
  maintenanceObservationCount: 0,
  monitoringPaused: false,
  inMaintenance: false,
  notificationSummary: { delivered: 1, pending: 0, failed: 0 },
};
afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});
it('groups each section by application name, preserving order inside each group and placing unassigned last', () => {
  const grouped = groupByApplication([
    { monitor: { ...monitor, id: 'u', name: 'Unassigned' } },
    {
      monitor: {
        ...monitor,
        id: 'b',
        name: 'Billing',
        applicationId: 'billing',
        applicationName: 'Billing',
      },
    },
    {
      monitor: {
        ...monitor,
        id: 'a',
        name: 'Accounts',
        applicationId: 'accounts',
        applicationName: 'Accounts',
      },
    },
    {
      monitor: {
        ...monitor,
        id: 'b2',
        name: 'Billing two',
        applicationId: 'billing',
        applicationName: 'Billing',
      },
    },
  ]);
  expect(grouped.map((group) => group.name)).toEqual(['Accounts', 'Billing', 'No application']);
  expect(grouped[1].items.map((item) => item.monitor.id)).toEqual(['b', 'b2']);
});
describe('Overview', () => {
  it('renders application headings above grouped problem items, with unassigned last', async () => {
    vi.mocked(getOverview).mockResolvedValue({
      ...data,
      failingWithoutIncident: [
        { monitor, status: monitorStatusFixture({ state: 'failing', reason: null }) },
        {
          monitor: {
            ...monitor,
            id: 'm2',
            name: 'Billing check',
            applicationId: 'billing',
            applicationName: 'Billing',
          },
          status: monitorStatusFixture({ state: 'failing', reason: null }),
        },
      ],
    });
    render(<OverviewPage />);
    const section = await screen.findByRole('region', { name: 'Failing — incident not open yet' });
    const headings = Array.from(section.querySelectorAll('h4')).map(
      (element) => element.textContent,
    );
    expect(headings).toEqual(['Billing', 'No application']);
    expect(section.querySelectorAll('li')[0]).toHaveTextContent('Billing check');
  });
  it('shows All clear above outages and recent recoveries when problem sections are empty', async () => {
    vi.mocked(getOverview).mockResolvedValue({
      ...data,
      receiveOutages: [{ from: '2026-09-28T11:00:00.000Z', to: '2026-09-28T11:05:00.000Z' }],
      recentRecoveries: [{ monitor, incident }],
    });
    render(<OverviewPage />);
    expect(
      await screen.findByText(/All clear — 2 active monitors checked recently; 1 paused/),
    ).toBeInTheDocument();
    const allClear = screen.getByText(/All clear — 2 active monitors checked recently; 1 paused/);
    const outage = screen.getByRole('heading', { name: 'StatusForge was not receiving' });
    const recovery = screen.getByRole('heading', { name: 'Recovered in the last 24 hours' });
    expect(
      allClear.compareDocumentPosition(outage) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      allClear.compareDocumentPosition(recovery) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Coverage problems' })).not.toBeInTheDocument();
  });
  it('keeps the sections in contract order, hiding empty ones and suppressing All clear', async () => {
    vi.mocked(getOverview).mockResolvedValue({
      ...data,
      receiveOutages: [{ from: '2026-09-28T11:00:00.000Z', to: null }],
      failingWithoutIncident: [
        { monitor, status: monitorStatusFixture({ state: 'failing', reason: null }) },
      ],
      coverageProblems: [
        {
          monitor: { ...monitor, id: 'm2', name: 'Other' },
          status: monitorStatusFixture({ state: 'checker_problem', reason: null }),
        },
      ],
      notifications: { count: 1, items: [] },
    });
    render(<OverviewPage />);
    expect(
      await screen.findByRole('heading', { name: 'Notifications need attention' }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole('heading', { level: 3 }).map((heading) => heading.textContent),
    ).toEqual([
      'StatusForge was not receiving',
      'Failing — incident not open yet',
      'Coverage problems',
      'Notifications need attention',
    ]);
    expect(screen.getByText(/not a target failure/)).toBeInTheDocument();
    expect(screen.queryByText(/All clear/)).not.toBeInTheDocument();
  });
  it('does not show All clear when an open incident is the only problem', async () => {
    vi.mocked(getOverview).mockResolvedValue({
      ...data,
      openIncidents: [
        { monitor, incident: { ...incident, state: 'open', resolution: null, resolvedAt: null } },
      ],
    });
    render(<OverviewPage />);
    expect(await screen.findByRole('heading', { name: 'Open incidents' })).toBeInTheDocument();
    expect(screen.queryByText(/All clear/)).not.toBeInTheDocument();
  });
  it('uses singular monitor for a single active monitor', async () => {
    vi.mocked(getOverview).mockResolvedValue({
      ...data,
      counts: { ...data.counts, active: 1, paused: 1 },
    });
    render(<OverviewPage />);
    expect(
      await screen.findByText(/All clear — 1 active monitor checked recently; 1 paused/),
    ).toBeInTheDocument();
  });
  it('says the scheduler is behind in words, with guidance, instead of All clear', async () => {
    vi.mocked(getOverview).mockResolvedValue({
      ...data,
      receiveOutages: [{ from: '2026-09-28T11:00:00.000Z', to: '2026-09-28T11:05:00.000Z' }],
      scheduler: { state: 'behind', windowMinutes: 5, dueChecks: 40, missedChecks: 9, workers: 4 },
    });
    render(<OverviewPage />);
    const section = await screen.findByRole('region', { name: 'Scheduler is behind' });
    expect(section).toHaveTextContent(
      'Scheduler is behind — 9 of 40 due checks in the last 5 minutes were missed because workers were busy.',
    );
    expect(section).toHaveTextContent(
      'To keep up, use longer check intervals or monitor fewer targets. If checks wait on slow targets, raising STATUSFORGE_WORKERS (now 4; up to 16) can also help.',
    );
    expect(
      screen.getAllByRole('heading', { level: 3 }).map((heading) => heading.textContent),
    ).toEqual(['StatusForge was not receiving', 'Scheduler is behind']);
    expect(screen.queryByText(/All clear/)).not.toBeInTheDocument();
  });
  it.each([
    { state: 'unknown' as const, note: null },
    { state: 'ok' as const, note: null },
    { state: 'disabled' as const, note: /Scheduled checks are turned off/ },
  ])('shows no behind item when the scheduler is $state', async ({ state, note }) => {
    vi.mocked(getOverview).mockResolvedValue({
      ...data,
      scheduler: { ...data.scheduler, state, dueChecks: 0 },
    });
    render(<OverviewPage />);
    expect(await screen.findByText(/All clear/)).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Scheduler is behind' })).not.toBeInTheDocument();
    if (note === null) {
      expect(screen.queryByText(/Scheduled checks are turned off/)).not.toBeInTheDocument();
    } else {
      expect(screen.getByText(note)).toHaveTextContent(
        'Scheduled checks are turned off (STATUSFORGE_SCHEDULER_ENABLED=false); only manual checks run.',
      );
    }
  });
  it('polls one overview request every 15 s while visible, not one per monitor', async () => {
    vi.mocked(getOverview).mockResolvedValue(data);
    vi.useFakeTimers();
    render(<OverviewPage />);
    await act(async () => {
      await Promise.resolve();
    });
    expect(getOverview).toHaveBeenCalledTimes(1);
    await act(async () => {
      vi.advanceTimersByTime(15_000);
    });
    expect(getOverview).toHaveBeenCalledTimes(2);
  });
});

it('labels an application ID missing from the application list as deleted', async () => {
  vi.mocked(getOverview).mockResolvedValue({
    ...data,
    failingWithoutIncident: [
      {
        monitor: { ...monitor, applicationId: 'gone', applicationName: 'Old name' },
        status: monitorStatusFixture({ state: 'failing', reason: null }),
      },
    ],
  });
  render(<OverviewPage />);
  expect(await screen.findByRole('heading', { name: 'Deleted application' })).toBeInTheDocument();
  expect(screen.queryByRole('link', { name: 'Old name' })).not.toBeInTheDocument();
});
