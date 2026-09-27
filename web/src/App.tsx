import { BackendStatus } from './components/BackendStatus';
import { MonitorCreatePage } from './pages/MonitorCreatePage';
import { MonitorDetailPage } from './pages/MonitorDetailPage';
import { MonitorListPage } from './pages/MonitorListPage';
import { Link } from './router/Link';
import { usePathname } from './router/history';
import { matchRoute } from './router/routes';

export default function App() {
  const pathname = usePathname();
  const route = matchRoute(pathname);

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
              <Link to="/" aria-current={route.name === 'monitors' ? 'page' : undefined}>
                Monitors
              </Link>
            </li>
            <li>
              <Link
                to="/monitors/new"
                aria-current={route.name === 'create-monitor' ? 'page' : undefined}
              >
                New monitor
              </Link>
            </li>
          </ul>
        </nav>
        <BackendStatus />
      </header>

      <main className="app-main">
        {route.name === 'monitors' ? <MonitorListPage /> : null}
        {route.name === 'create-monitor' ? <MonitorCreatePage /> : null}
        {route.name === 'monitor-detail' ? (
          <MonitorDetailPage key={route.monitorId} monitorId={route.monitorId} />
        ) : null}
        {route.name === 'not-found' ? (
          <section aria-labelledby="page-not-found-heading">
            <h2 id="page-not-found-heading">Page not found</h2>
            <p>That address does not match a StatusForge page.</p>
            <p>
              <Link to="/">Back to monitors</Link>
            </p>
          </section>
        ) : null}
      </main>
    </>
  );
}
