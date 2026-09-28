import { describe, expect, it } from 'vitest';
import { matchRoute } from './routes';

describe('matchRoute', () => {
  it.each([
    ['/', 'overview'],
    ['/monitors', 'monitors'],
    ['/monitors/', 'monitors'],
    ['/monitors/new', 'create-monitor'],
    ['/monitors/monitor-1', 'monitor-detail'],
    ['/monitors/monitor-1/', 'monitor-detail'],
    ['/incidents', 'incidents'],
    ['/incidents/monitor-1/incident-1', 'incident-detail'],
    ['/incidents/monitor-1/incident-1/', 'incident-detail'],
    ['/incidents/%E0%A4%A/incident', 'not-found'],
    ['/unknown', 'not-found'],
    ['/monitors/monitor-1/observations', 'not-found'],
    ['/monitors/%E0%A4%A', 'not-found'],
  ])('maps %s to %s', (pathname, name) => {
    expect(matchRoute(pathname).name).toBe(name);
  });

  it('decodes the monitor id', () => {
    expect(matchRoute('/monitors/monitor%2F1')).toEqual({
      name: 'monitor-detail',
      monitorId: 'monitor/1',
    });
  });
  it('decodes both incident route identifiers', () => {
    expect(matchRoute('/incidents/monitor%2F1/incident%2F1')).toEqual({
      name: 'incident-detail',
      monitorId: 'monitor/1',
      incidentId: 'incident/1',
    });
  });
});
