import { useEffect, useRef, useState } from 'react';

export function HeartbeatTokenPanel({
  token,
  ingestPath,
  onDismiss,
  applicationId,
  focusOnShow = false,
}: {
  token: string;
  ingestPath: string;
  onDismiss: () => void;
  applicationId?: string;
  focusOnShow?: boolean;
}) {
  const [notice, setNotice] = useState('');
  const copyRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (applicationId || focusOnShow) copyRef.current?.focus();
  }, [applicationId, focusOnShow, token]);
  const command = applicationId
    ? `curl -fsS -X POST -H "Authorization: Bearer ${token}" -H "Content-Type: application/json" -d '{"version":"1.2.3"}' http://127.0.0.1:8080/ingest/applications/${encodeURIComponent(applicationId)}/deployments`
    : `curl -fsS -X POST -H "Authorization: Bearer ${token}" http://127.0.0.1:8080${ingestPath}`;
  return (
    <section
      className="panel token-panel"
      aria-label={applicationId ? 'New application token' : 'New heartbeat token'}
    >
      <h3>{applicationId ? 'Application' : 'Heartbeat'} token — Shown once</h3>
      <p>Copy this token now. It will not be available again after you leave this page.</p>
      <code>{token}</code>
      <button
        ref={copyRef}
        type="button"
        className="button"
        onClick={() => {
          void navigator.clipboard.writeText(token).then(
            () => setNotice('Token copied.'),
            () => setNotice('Could not copy; select the token above.'),
          );
        }}
      >
        Copy token
      </button>
      <p role="status">{notice}</p>
      <p>Example {applicationId ? 'deployment' : 'report'}:</p>
      <pre>
        <code>{command}</code>
      </pre>
      <button type="button" className="button" onClick={onDismiss}>
        Done
      </button>
    </section>
  );
}
