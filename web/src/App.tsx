import { BackendStatus } from './components/BackendStatus';

export default function App() {
  return (
    <>
      <header className="app-header">
        <h1>StatusForge</h1>
        <p className="tagline">
          Know what is working. Understand what failed. Never confuse silence with health.
        </p>
      </header>
      <main className="app-main">
        <p>
          This is the M0 foundation shell. No monitors exist yet, so the only thing this page can
          report today is whether the local backend answers a readiness check.
        </p>
        <BackendStatus />
      </main>
    </>
  );
}
