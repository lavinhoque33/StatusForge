import { useEffect, useRef, useState } from 'react';
import {
  getDeletion,
  requestDeletion,
  type DeletionResource,
  type DeletionStatus,
} from '../api/deletion';
import { ApiRequestError, ApiValidationError, isAbortError } from '../api/http';
import { describeApiError, fieldErrorMessage } from '../lib/errors';

export function PermanentDeletion({
  resource,
  id,
  name,
  deletion,
  onStatus,
  onComplete,
}: {
  resource: DeletionResource;
  id: string;
  name: string;
  deletion: DeletionStatus | null;
  onStatus: (status: DeletionStatus) => void;
  onComplete: () => void;
}) {
  const [confirm, setConfirm] = useState(false);
  const [typed, setTyped] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const triggerRef = useRef<HTMLButtonElement>(null);
  const callbacks = useRef({ onStatus, onComplete });
  useEffect(() => {
    callbacks.current = { onStatus, onComplete };
  }, [onStatus, onComplete]);
  const inputRef = useRef<HTMLInputElement>(null);
  const [status, setStatus] = useState<DeletionStatus | null>(null);
  const progress = status ?? deletion;
  const polling = progress !== null;
  useEffect(() => {
    if (confirm) inputRef.current?.focus();
  }, [confirm]);
  useEffect(() => {
    if (!polling) return;
    const controller = new AbortController();
    let running = false;
    const timer = window.setInterval(async () => {
      if (running || document.visibilityState === 'hidden') return;
      running = true;
      try {
        const next = await getDeletion(resource, id, controller.signal);
        if (controller.signal.aborted) return;
        setStatus(next);
        setError('');
        callbacks.current.onStatus(next);
      } catch (cause) {
        if (controller.signal.aborted || isAbortError(cause)) return;
        if (cause instanceof ApiRequestError && cause.status === 404) {
          callbacks.current.onComplete();
          return;
        }
        setError(`Could not refresh deletion progress: ${describeApiError(cause)}`);
      } finally {
        running = false;
      }
    }, 2000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [polling, resource, id]);
  const cancel = () => {
    setConfirm(false);
    setTyped('');
    setError('');
    window.setTimeout(() => triggerRef.current?.focus(), 0);
  };
  const submit = async () => {
    if (typed !== name || pending) return;
    setPending(true);
    setError('');
    try {
      const next = await requestDeletion(resource, id, typed);
      setStatus(next);
      onStatus(next);
      setConfirm(false);
      setTyped('');
    } catch (cause) {
      if (cause instanceof ApiValidationError && cause.fields.confirmName) {
        setError(fieldErrorMessage(cause.fields.confirmName));
      } else {
        setError(describeApiError(cause));
      }
    } finally {
      setPending(false);
    }
  };
  return (
    <section className="panel" aria-label={`Permanently delete ${name}`}>
      <h3>Delete permanently</h3>
      <p>
        {resource === 'monitors'
          ? 'All checks, gap records, incidents, their notifications, maintenance windows, and pause history for this monitor will be removed. Application deployment markers are separate and remain available.'
          : 'All deployment markers and duplicate guards for this application will be removed. Monitor checks, gaps, incidents, notifications, maintenance windows, and pause history remain on their monitors; incident references to this application become Deleted application.'}{' '}
        This cannot be undone.
      </p>
      <p>
        Export your data first. See <code>docs/operations/local-runbook.md</code> for export and
        deletion instructions.
      </p>
      {progress !== null ? (
        <p role="status" aria-live="polite">
          {progress.state === 'waiting_for_notifications'
            ? 'Waiting for pending notifications to finish'
            : `Deleting — ${progress.removedItems} records removed`}
        </p>
      ) : confirm ? (
        <form
          className="confirm-step"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <label htmlFor={`delete-${resource}-${id}`}>Type {name} to delete permanently</label>
          <input
            ref={inputRef}
            id={`delete-${resource}-${id}`}
            value={typed}
            autoComplete="off"
            onChange={(event) => setTyped(event.target.value)}
            aria-invalid={!!error}
            aria-describedby={error ? `delete-error-${resource}-${id}` : undefined}
          />
          <button type="submit" className="button" disabled={pending || typed !== name}>
            Confirm permanent deletion
          </button>
          <button type="button" className="button" disabled={pending} onClick={cancel}>
            Cancel
          </button>
        </form>
      ) : (
        <button ref={triggerRef} type="button" className="button" onClick={() => setConfirm(true)}>
          Delete permanently
        </button>
      )}
      {error ? (
        <p role="alert" id={`delete-error-${resource}-${id}`}>
          {error}
        </p>
      ) : null}
    </section>
  );
}
