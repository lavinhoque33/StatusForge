import { useEffect, useRef, useState, type MouseEvent } from 'react';
import { getSystem, type SystemInfo } from './api/system';
import { AttentionBanner } from './components/AttentionBanner';
import { Icon, type IconName } from './components/Icon';
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

/** Main navigation: label (the link's exact accessible name), icon, active routes. */
const navItems: Array<{ to: string; label: string; icon: IconName; routes: Route['name'][] }> = [
  { to: '/', label: 'Overview', icon: 'overview', routes: ['overview'] },
  {
    to: '/monitors',
    label: 'Monitors',
    icon: 'monitors',
    routes: ['monitors', 'create-monitor', 'monitor-detail'],
  },
  {
    to: '/incidents',
    label: 'Incidents',
    icon: 'incidents',
    routes: ['incidents', 'incident-detail'],
  },
  { to: '/activity', label: 'Activity', icon: 'activity', routes: ['activity'] },
  { to: '/applications', label: 'Applications', icon: 'applications', routes: ['applications'] },
  { to: '/settings', label: 'Settings', icon: 'settings', routes: ['settings'] },
];

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

  const skipToContent = (event: MouseEvent<HTMLAnchorElement>) => {
    const main = document.getElementById('main');
    if (!main) return;
    event.preventDefault();
    const heading = main.querySelector<HTMLHeadingElement>('h2');
    const target: HTMLElement = heading ?? main;
    target.tabIndex = -1;
    target.focus();
    target.scrollIntoView({ block: 'start' });
  };

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main" onClick={skipToContent}>
        Skip to content
      </a>
      <header className="app-header">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">
            <Icon name="pulse" size={20} />
          </span>
          <h1>StatusForge</h1>
          <p className="tagline">
            Know what is working. Understand what failed. Never confuse silence with health.
          </p>
        </div>
        <nav aria-label="Main">
          <ul className="nav-list">
            {navItems.map((item) => (
              <li key={item.to}>
                <Link
                  to={item.to}
                  className="nav-link"
                  aria-current={item.routes.includes(route.name) ? 'page' : undefined}
                >
                  <Icon name={item.icon} />
                  <span>{item.label}</span>
                </Link>
              </li>
            ))}
          </ul>
        </nav>
        <BackendStatus />
      </header>

      <div className="app-content">
        <div className="app-alerts">
          {identity?.demo ? (
            <p className="demo-banner" role="status">
              Demo data
            </p>
          ) : null}
          <AttentionBanner />
        </div>
        <main className="app-main" id="main" ref={mainRef}>
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
            <section className="empty-state" aria-labelledby="page-not-found-heading">
              <span className="empty-icon" aria-hidden="true">
                <Icon name="compass" size={22} />
              </span>
              <h2 id="page-not-found-heading">Page not found</h2>
              <p>That address does not match a StatusForge page.</p>
              <p>
                <Link to="/monitors" className="back-link">
                  <Icon name="arrow-left" size={16} />
                  Back to monitors
                </Link>
              </p>
            </section>
          ) : null}
        </main>
        <footer className="app-footer">
          <small>{identity ? <>Version {identity.version}</> : 'Version unavailable'}</small>
        </footer>
      </div>
    </div>
  );
}
