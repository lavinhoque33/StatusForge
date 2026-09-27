/** Route matching for the M1 paths; anything else is not found. */
export type Route =
  | { name: 'monitors' }
  | { name: 'create-monitor' }
  | { name: 'monitor-detail'; monitorId: string }
  | { name: 'not-found' };

export function matchRoute(pathname: string): Route {
  const path = pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
  if (path === '/') return { name: 'monitors' };

  const segments = path.split('/').filter((segment) => segment !== '');
  if (segments.length === 2 && segments[0] === 'monitors') {
    if (segments[1] === 'new') return { name: 'create-monitor' };
    try {
      return { name: 'monitor-detail', monitorId: decodeURIComponent(segments[1]) };
    } catch {
      return { name: 'not-found' };
    }
  }
  return { name: 'not-found' };
}
