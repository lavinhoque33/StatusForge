import { useState } from 'react';

export function HeartbeatTokenPanel({
  token,
  ingestPath,
  onDismiss,
}: {
  token: string;
  ingestPath: string;
  onDismiss: () => void;
}) {
  const [notice, setNotice] = useState('');
  const command = `curl -fsS -X POST -H "Authorization: Bearer ${token}" http://127.0.0.1:8080${ingestPath}`;
  return (
    <section className="panel token-panel" aria-label="New heartbeat token">
      <h3>Heartbeat token — Shown once</h3>
      <p>Copy this token now. It will not be available again after you leave this page.</p>
      <code>{token}</code>
      <button
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
      <p>Example report:</p>
      <pre>
        <code>{command}</code>
      </pre>
      <button type="button" className="button" onClick={onDismiss}>
        Done
      </button>
    </section>
  );
}
