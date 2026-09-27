import { useCallback, useEffect, useRef, useState } from 'react';
import {
  HealthInvalidResponseError,
  HealthTimeoutError,
  fetchReadiness,
  type DependencyReason,
  type DependencyStatus,
  type Readiness,
} from '../api/health';

/** Deadline for a single readiness check. */
const CHECK_TIMEOUT_MS = 5000;

type CheckState =
  | { name: 'checking' }
  | { name: 'ready' | 'degraded'; readiness: Readiness }
  | { name: 'unreachable' | 'invalid' | 'timeout' };

const STATUS_TEXT: Record<CheckState['name'], string> = {
  checking: 'Checking...',
  ready: 'Ready',
  degraded: 'Degraded',
  unreachable: 'Unreachable',
  invalid: 'Unexpected response',
  timeout: `Timed out after ${CHECK_TIMEOUT_MS / 1000} s`,
};

const DEPENDENCY_STATUS_TEXT: Record<DependencyStatus['status'], string> = {
  ready: 'Ready',
  unavailable: 'Unavailable',
};

const REASON_TEXT: Record<DependencyReason, string> = {
  timeout: 'timed out',
  unreachable: 'unreachable',
  error: 'reported an error',
};

/**
 * Backend readiness panel: one check on mount, manual re-checks, no polling.
 * Every request is bounded by `CHECK_TIMEOUT_MS` and cancelled on unmount.
 */
export function BackendStatus() {
  const [state, setState] = useState<CheckState>({ name: 'checking' });
  const controllerRef = useRef<AbortController | null>(null);

  // Starts a bounded request without touching state: the mounting effect must
  // not set state synchronously, and the initial state is already `checking`.
  const startCheck = useCallback(() => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;

    const timer = window.setTimeout(() => {
      controller.abort(
        new DOMException(`No response within ${CHECK_TIMEOUT_MS / 1000} s`, 'TimeoutError'),
      );
    }, CHECK_TIMEOUT_MS);

    fetchReadiness(controller.signal)
      .then((readiness) => {
        if (controllerRef.current !== controller) return;
        setState({ name: readiness.status, readiness });
      })
      .catch((error: unknown) => {
        // A superseded request (or one cancelled by unmount) stays silent.
        if (controllerRef.current !== controller) return;
        if (error instanceof HealthTimeoutError) {
          setState({ name: 'timeout' });
        } else if (error instanceof HealthInvalidResponseError) {
          setState({ name: 'invalid' });
        } else {
          setState({ name: 'unreachable' });
        }
      })
      .finally(() => {
        window.clearTimeout(timer);
      });
  }, []);

  useEffect(() => {
    startCheck();
    return () => {
      controllerRef.current?.abort();
      controllerRef.current = null;
    };
  }, [startCheck]);

  const checkAgain = () => {
    setState({ name: 'checking' });
    startCheck();
  };

  const answered = state.name === 'ready' || state.name === 'degraded';
  const dependencies = answered ? Object.entries(state.readiness.dependencies) : [];
  // The backend's observation instant, shown in the reader's local time zone.
  const observedAt = answered ? new Date(state.readiness.checkedAt) : null;

  return (
    <section className="panel" aria-labelledby="backend-status-heading">
      <h2 id="backend-status-heading">Backend status</h2>
      <p className={`status status--${state.name}`} role="status" aria-live="polite">
        <span className="status-badge">{STATUS_TEXT[state.name]}</span>
      </p>
      {answered && observedAt ? (
        <p className="checked-at">
          Checked at{' '}
          <time dateTime={state.readiness.checkedAt}>
            {observedAt.toLocaleTimeString([], { hour12: false })}
          </time>{' '}
          (local time)
        </p>
      ) : null}
      {answered && dependencies.length === 0 ? <p>No dependencies were reported.</p> : null}
      {answered && dependencies.length > 0 ? (
        <>
          <h3>Dependencies</h3>
          <ul className="dependencies">
            {dependencies.map(([name, dependency]) => (
              <li key={name}>
                <strong>{name}</strong>
                {`: ${DEPENDENCY_STATUS_TEXT[dependency.status]}`}
                {dependency.reason ? ` (${REASON_TEXT[dependency.reason]})` : null}
              </li>
            ))}
          </ul>
        </>
      ) : null}
      <button type="button" className="check-again" onClick={checkAgain}>
        Check again
      </button>
    </section>
  );
}

export default BackendStatus;
