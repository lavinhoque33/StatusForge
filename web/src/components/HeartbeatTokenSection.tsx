import { useRef, useState } from 'react';
import type { HeartbeatConfig } from '../api/monitors';
import { revokeHeartbeatToken, rotateHeartbeatToken } from '../api/monitors';
import { describeApiError } from '../lib/errors';
import { formatLocalWithOffset } from '../lib/time';
import { HeartbeatTokenPanel } from './HeartbeatTokenPanel';

export function HeartbeatTokenSection({
  id,
  heartbeat,
  archived,
  refresh,
}: {
  id: string;
  heartbeat: HeartbeatConfig;
  archived: boolean;
  refresh: () => void;
}) {
  const [confirm, setConfirm] = useState<'rotate' | 'revoke' | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [message, setMessage] = useState('');
  const [pending, setPending] = useState(false);
  const rotateRef = useRef<HTMLButtonElement>(null);
  const revokeRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const act = async () => {
    if (confirm === null) return;
    setPending(true);
    setMessage('');
    const action = confirm;
    let revoked = false;
    try {
      if (action === 'rotate') {
        const issued = await rotateHeartbeatToken(id);
        setToken(issued.token);
        setMessage('Token rotated. The previous token no longer works.');
      } else {
        await revokeHeartbeatToken(id);
        setToken(null);
        setMessage('Token revoked. Reports require a new token.');
        revoked = true;
      }
      refresh();
    } catch (error) {
      setMessage(describeApiError(error));
    } finally {
      setPending(false);
      setConfirm(null);
      window.setTimeout(
        () => (action === 'rotate' || revoked ? rotateRef : revokeRef).current?.focus(),
        0,
      );
    }
  };
  return (
    <section className="panel" aria-labelledby="heartbeat-token-heading">
      <h3 id="heartbeat-token-heading">Token</h3>
      {heartbeat.token === null ? (
        <p>No active token.</p>
      ) : (
        <p>
          Hint: …{heartbeat.token.hint} · Created{' '}
          <time dateTime={heartbeat.token.createdAt}>
            {formatLocalWithOffset(new Date(heartbeat.token.createdAt))}
          </time>
        </p>
      )}
      {token !== null ? (
        <HeartbeatTokenPanel
          token={token}
          ingestPath={heartbeat.ingestPath}
          onDismiss={() => {
            setToken(null);
            rotateRef.current?.focus();
          }}
        />
      ) : null}
      {archived ? null : (
        <div className="actions">
          <button
            ref={rotateRef}
            type="button"
            className="button"
            onClick={() => {
              setConfirm('rotate');
              window.setTimeout(() => confirmRef.current?.focus(), 0);
            }}
          >
            Rotate token
          </button>
          {heartbeat.token !== null ? (
            <button
              ref={revokeRef}
              type="button"
              className="button"
              onClick={() => {
                setConfirm('revoke');
                window.setTimeout(() => confirmRef.current?.focus(), 0);
              }}
            >
              Revoke token
            </button>
          ) : null}
        </div>
      )}
      {confirm === null ? null : (
        <div className="confirm-step">
          <p>
            {confirm === 'rotate'
              ? 'Rotate the token? The old token will stop working immediately.'
              : 'Revoke the token? Reports will be refused until a new token is issued.'}
          </p>
          <button
            ref={confirmRef}
            type="button"
            className="button"
            disabled={pending}
            onClick={() => void act()}
          >
            Confirm {confirm}
          </button>
          <button
            type="button"
            className="button"
            disabled={pending}
            onClick={() => {
              const previous = confirm;
              setConfirm(null);
              window.setTimeout(
                () => (previous === 'rotate' ? rotateRef : revokeRef).current?.focus(),
                0,
              );
            }}
          >
            Cancel
          </button>
        </div>
      )}
      {message ? <p role="status">{message}</p> : null}
    </section>
  );
}
