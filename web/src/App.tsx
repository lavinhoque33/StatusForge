import { useEffect, useRef, useState } from 'react';
import { getSystem, type SystemInfo } from './api/system';
import { AttentionBanner } from './components/AttentionBanner';
import { IncidentDetailPage } from './pages/IncidentDetailPage';
import { IncidentsPage } from './pages/IncidentsPage';
import { ActivityPage } from './pages/ActivityPage';
import { ApplicationsPage } from './pages/ApplicationsPage';
import { BackendStatus } from './components/BackendStatus';
import { MonitorCreatePage } from './pages/MonitorCreatePage';
import { MonitorDetailPage } from './pages/MonitorDetailPage';
import { MonitorListPage } from './pages/MonitorListPage';
import { OverviewPage } from './pages/OverviewPage';
import { SettingsPage } from './pages/SettingsPage';
import { Link } from './router/Link';
import { usePathname } from './router/history';
import { matchRoute, type Route } from './router/routes';

const pageNames: Record<Route['name'], string> = {
  overview: 'Overview',
  monitors: 'Monitors',
  'create-monitor': 'New monitor',
  'monitor-detail': 'Monitor',
  incidents: 'Incidents',
  'incident-detail': 'Incident',
  activity: 'Activity',
  applications: 'Applications',
  settings: 'Settings',
  'not-found': 'Page not found',
};

export default function App() {
  const pathname = usePathname();
  const route = matchRoute(pathname);
  const routeName = route.name;
  const previousPathname = useRef(pathname);
  const mainRef = useRef<HTMLElement>(null);
  useEffect(() => {
    const navigating = previousPathname.current !== pathname;
    previousPathname.current = pathname;
    const main = mainRef.current;
    if (!main) return;

    document.title = `${pageNames[routeName]} — StatusForge`;
    let focused = !navigating;
    const updateHeading = () => {
      const heading = main.querySelector<HTMLHeadingElement>('h2');
      // The incident loader has a temporary heading; wait for its real heading
      // so focus does not fall back to body when the loader is replaced.
      if (
        !heading ||
        (routeName === 'incident-detail' &&
          heading.textContent === 'Incident' &&
          main.querySelector('[role="status"]'))
      )
        return false;
      heading.tabIndex = -1;
      document.title = `${heading.textContent?.trim() || pageNames[routeName]} — StatusForge`;
      if (!focused) {
        focused = true;
        // A component may intentionally manage focus during a transition.
        if (!main.contains(document.activeElement) || document.activeElement === document.body) {
          heading.focus();
        }
      }
      return true;
    };
    if (updateHeading()) return;
    const observer = new MutationObserver(() => {
      if (updateHeading()) observer.disconnect();
    });
    observer.observe(main, { childList: true, subtree: true, characterData: true });
    return () => observer.disconnect();
  }, [pathname, routeName]);

  const [identity, setIdentity] = useState<Pick<SystemInfo, 'version' | 'demo'> | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    void getSystem(controller.signal)
      .then(setIdentity)
      .catch(() => setIdentity(null));
    return () => controller.abort();
  }, []);

  return (
    <>
      <header className="app-header">
        <h1>StatusForge</h1>
        <p className="tagline">
          Know what is working. Understand what failed. Never confuse silence with health.
        </p>
        <nav aria-label="Main">
          <ul>
            <li>
              <Link to="/" aria-current={route.name === 'overview' ? 'page' : undefined}>
                Overview
              </Link>
            </li>
            <li>
              <Link
                to="/monitors"
                aria-current={
                  ['monitors', 'create-monitor', 'monitor-detail'].includes(route.name)
                    ? 'page'
                    : undefined
                }
              >
                Monitors
              </Link>
            </li>
            <li>
              <Link
                to="/incidents"
                aria-current={
                  route.name === 'incidents' || route.name === 'incident-detail'
                    ? 'page'
                    : undefined
                }
              >
                Incidents
              </Link>
            </li>
            <li>
              <Link to="/activity" aria-current={route.name === 'activity' ? 'page' : undefined}>
                Activity
              </Link>
            </li>
            <li>
              <Link
                to="/applications"
                aria-current={route.name === 'applications' ? 'page' : undefined}
              >
                Applications
              </Link>
            </li>
            <li>
              <Link to="/settings" aria-current={route.name === 'settings' ? 'page' : undefined}>
                Settings
              </Link>
            </li>
          </ul>
        </nav>
        {identity?.demo ? (
          <p className="demo-banner" role="status">
            Demo data
          </p>
        ) : null}
        <BackendStatus />
        <AttentionBanner />
      </header>

      <main className="app-main" ref={mainRef}>
        {route.name === 'overview' ? <OverviewPage /> : null}
        {route.name === 'monitors' ? <MonitorListPage /> : null}
        {route.name === 'create-monitor' ? <MonitorCreatePage /> : null}
        {route.name === 'monitor-detail' ? (
          <MonitorDetailPage key={route.monitorId} monitorId={route.monitorId} />
        ) : null}
        {route.name === 'incidents' ? <IncidentsPage /> : null}
        {route.name === 'activity' ? <ActivityPage /> : null}
        {route.name === 'applications' ? <ApplicationsPage /> : null}
        {route.name === 'settings' ? <SettingsPage /> : null}
        {route.name === 'incident-detail' ? (
          <IncidentDetailPage
            key={`${route.monitorId}:${route.incidentId}`}
            monitorId={route.monitorId}
            incidentId={route.incidentId}
          />
        ) : null}
        {route.name === 'not-found' ? (
          <section aria-labelledby="page-not-found-heading">
            <h2 id="page-not-found-heading">Page not found</h2>
            <p>That address does not match a StatusForge page.</p>
            <p>
              <Link to="/monitors">Back to monitors</Link>
            </p>
          </section>
        ) : null}
      </main>
      <footer className="app-footer">
        <small>{identity ? <>Version {identity.version}</> : 'Version unavailable'}</small>
      </footer>
    </>
  );
}
