import { describe, expect, it } from 'vitest';
import { matchRoute } from './routes';

describe('matchRoute', () => {
  it.each([
    ['/', 'monitors'],
    ['/monitors/new', 'create-monitor'],
    ['/monitors/monitor-1', 'monitor-detail'],
    ['/monitors/monitor-1/', 'monitor-detail'],
    ['/unknown', 'not-found'],
    ['/monitors', 'not-found'],
    ['/monitors/', 'not-found'],
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
});
