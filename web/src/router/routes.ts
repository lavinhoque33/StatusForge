/** Route matching for monitor and incident pages. */
export type Route =
  | { name: 'overview' }
  | { name: 'monitors' }
  | { name: 'activity' }
  | { name: 'applications' }
  | { name: 'settings' }
  | { name: 'create-monitor' }
  | { name: 'monitor-detail'; monitorId: string }
  | { name: 'incidents' }
  | { name: 'incident-detail'; monitorId: string; incidentId: string }
  | { name: 'not-found' };

export function matchRoute(pathname: string): Route {
  const path = pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
  if (path === '/') return { name: 'overview' };
  if (path === '/monitors') return { name: 'monitors' };
  if (path === '/incidents') return { name: 'incidents' };
  if (path === '/activity') return { name: 'activity' };
  if (path === '/applications') return { name: 'applications' };
  if (path === '/settings') return { name: 'settings' };

  const segments = path.split('/').filter((segment) => segment !== '');
  if (segments.length === 2 && segments[0] === 'monitors') {
    if (segments[1] === 'new') return { name: 'create-monitor' };
    try {
      return { name: 'monitor-detail', monitorId: decodeURIComponent(segments[1]) };
    } catch {
      return { name: 'not-found' };
    }
  }
  if (segments.length === 3 && segments[0] === 'incidents') {
    try {
      return {
        name: 'incident-detail',
        monitorId: decodeURIComponent(segments[1]),
        incidentId: decodeURIComponent(segments[2]),
      };
    } catch {
      return { name: 'not-found' };
    }
  }
  return { name: 'not-found' };
}
