import { expect, it } from 'vitest';
import type { Marker } from '../api/applications';
import { monitorRecordFixture } from '../test/fixtures';
import { applicationSummary } from './applicationSummary';

it('counts only this application’s member incidents and chooses latest deployment time, not response order', () => {
  const markers: Marker[] = [
    {
      id: 'old',
      applicationId: 'app-1',
      version: 'v1',
      description: null,
      link: null,
      deployedAt: '2026-09-27T10:00:00.000Z',
      deploymentId: null,
      source: 'manual',
      reportedAt: '2026-09-28T12:00:00.000Z',
    },
    {
      id: 'new',
      applicationId: 'app-1',
      version: 'v2',
      description: null,
      link: null,
      deployedAt: '2026-09-28T10:00:00.000Z',
      deploymentId: null,
      source: 'manual',
      reportedAt: '2026-09-28T10:00:00.000Z',
    },
  ];
  const summary = applicationSummary(
    'app-1',
    [
      monitorRecordFixture({
        id: 'first',
        applicationId: 'app-1',
        openIncident: { id: 'incident', openedAt: '2026-09-28T09:00:00.000Z' },
      }),
      monitorRecordFixture({ id: 'second', applicationId: 'app-1', lifecycle: 'paused' }),
      monitorRecordFixture({
        id: 'other',
        applicationId: null,
        openIncident: { id: 'incident2', openedAt: '2026-09-28T09:00:00.000Z' },
      }),
    ],
    markers,
  );
  expect(summary.members.map((monitor) => monitor.id)).toEqual(['first', 'second']);
  expect(summary.openIncidents).toBe(1);
  expect(summary.lastDeployment?.version).toBe('v2');
});
