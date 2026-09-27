import { useEffect, useRef, useState } from 'react';
import { ApiRequestError, ApiValidationError, isAbortError, type FieldIssue } from '../api/http';
import {
  OBSERVATION_LIMIT,
  changeLifecycle,
  getMonitor,
  listObservations,
  runCheck,
  updateMonitor,
  type LifecycleAction,
  type Monitor,
  type Observation,
} from '../api/monitors';
import { LifecycleBadge } from '../components/LifecycleBadge';
import { MonitorForm } from '../components/MonitorForm';
import { MonitorHeadline } from '../components/MonitorHeadline';
import { ObservationsTable } from '../components/ObservationsTable';
import { describeApiError } from '../lib/errors';
import {
  MONITOR_FIELD_PATHS,
  checkInputFromFields,
  monitorFormFields,
  splitFieldErrors,
  type MonitorFormFields,
} from '../lib/monitorForm';
import { formatDeadlineSeconds, formatLocalWithOffset } from '../lib/time';
import { useNow } from '../lib/useNow';
import { Link } from '../router/Link';

type LoadState =
  | { name: 'loading' }
  | { name: 'ready'; monitor: Monitor; observations: Observation[] }
  | { name: 'not-found' }
  | { name: 'error'; message: string };

type RunState =
  | { name: 'idle' }
  | { name: 'running' }
  | { name: 'notice'; message: string; offerReload?: boolean };

type EditNotice = { kind: 'saved' } | { kind: 'conflict' } | { kind: 'message'; text: string };

/**
 * Detail page: configuration (with its version), lifecycle actions, one manual
 * check, and the observation history for this monitor.
 *
 * Mutations are one request each; a `409` is surfaced with its contract message
 * and the monitor is re-read, never retried automatically.
 */
export function MonitorDetailPage({ monitorId }: { monitorId: string }) {
  const [load, setLoad] = useState<LoadState>({ name: 'loading' });
  const [reloadToken, setReloadToken] = useState(0);
  const [fields, setFields] = useState<MonitorFormFields | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, FieldIssue>>({});
  const [editNotice, setEditNotice] = useState<EditNotice | null>(null);
  const [savePending, setSavePending] = useState(false);
  const [runState, setRunState] = useState<RunState>({ name: 'idle' });
  const [lifecycleNotice, setLifecycleNotice] = useState<string | null>(null);
  const [lifecyclePending, setLifecyclePending] = useState<LifecycleAction | null>(null);
  const [archiveConfirm, setArchiveConfirm] = useState(false);
  const now = useNow();
  const checkController = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    Promise.all([
      getMonitor(monitorId, controller.signal),
      listObservations(monitorId, OBSERVATION_LIMIT, controller.signal),
    ])
      .then(([monitor, observations]) => {
        if (controller.signal.aborted) return;
        setLoad({ name: 'ready', monitor, observations });
        setFields((current) => current ?? monitorFormFields(monitor));
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted || isAbortError(error)) return;
        if (error instanceof ApiRequestError && error.code === 'monitor_not_found') {
          setLoad({ name: 'not-found' });
          return;
        }
        setLoad({ name: 'error', message: describeApiError(error) });
      });
    return () => controller.abort();
  }, [monitorId, reloadToken]);

  useEffect(() => () => checkController.current?.abort(), []);

  const reload = () => {
    setReloadToken((token) => token + 1);
  };

  const startCheck = () => {
    if (load.name !== 'ready') return;
    const { monitor } = load;
    const controller = new AbortController();
    checkController.current?.abort();
    checkController.current = controller;
    setRunState({ name: 'running' });

    runCheck(monitor.id, controller.signal)
      .then((observation) => {
        if (checkController.current !== controller) return;
        checkController.current = null;
        setRunState({ name: 'idle' });
        setLoad((current) =>
          current.name === 'ready'
            ? { ...current, observations: [observation, ...current.observations] }
            : current,
        );
      })
      .catch((error: unknown) => {
        if (checkController.current !== controller || isAbortError(error)) return;
        checkController.current = null;
        if (error instanceof ApiRequestError && error.code === 'check_in_progress') {
          setRunState({ name: 'notice', message: 'A check is already running.' });
          return;
        }
        if (error instanceof ApiRequestError && error.code === 'archived') {
          setRunState({
            name: 'notice',
            message: 'This monitor is archived; checks are not allowed.',
          });
          reload();
          return;
        }
        if (error instanceof ApiRequestError && error.code === 'store_unavailable') {
          // The check may have run, but nothing was stored: never show an outcome.
          setRunState({
            name: 'notice',
            message: 'The check ran but could not be recorded.',
            offerReload: true,
          });
          return;
        }
        setRunState({ name: 'notice', message: describeApiError(error), offerReload: true });
      });
  };

  // A check in flight keeps the button focusable (`aria-disabled`) so keyboard
  // users do not lose their place; a second activation is a no-op.
  const startCheckIfIdle = () => {
    if (runState.name === 'running') return;
    if (load.name !== 'ready' || load.monitor.lifecycle === 'archived') return;
    startCheck();
  };

  const saveConfiguration = () => {
    if (load.name !== 'ready' || fields === null) return;
    const { monitor } = load;
    setSavePending(true);
    setFieldErrors({});
    setEditNotice(null);

    updateMonitor(monitor.id, {
      expectedConfigVersion: monitor.configVersion,
      name: fields.name,
      check: checkInputFromFields(fields),
    })
      .then((updated) => {
        setSavePending(false);
        setLoad((current) =>
          current.name === 'ready' ? { ...current, monitor: updated } : current,
        );
        setEditNotice({ kind: 'saved' });
      })
      .catch((error: unknown) => {
        if (isAbortError(error)) return;
        setSavePending(false);
        if (error instanceof ApiValidationError) {
          const split = splitFieldErrors(error.fields, MONITOR_FIELD_PATHS);
          setFieldErrors(split.fieldErrors);
          setEditNotice(
            split.formMessage === null ? null : { kind: 'message', text: split.formMessage },
          );
          return;
        }
        if (error instanceof ApiRequestError && error.code === 'version_conflict') {
          setEditNotice({ kind: 'conflict' });
          reload();
          return;
        }
        setEditNotice({ kind: 'message', text: describeApiError(error) });
      });
  };

  const applyLifecycle = (action: LifecycleAction) => {
    if (load.name !== 'ready') return;
    setLifecyclePending(action);
    setLifecycleNotice(null);

    changeLifecycle(load.monitor.id, action)
      .then((updated) => {
        setLifecyclePending(null);
        setArchiveConfirm(false);
        setLoad((current) =>
          current.name === 'ready' ? { ...current, monitor: updated } : current,
        );
        setLifecycleNotice(
          action === 'pause'
            ? 'Monitor paused.'
            : action === 'resume'
              ? 'Monitor resumed.'
              : 'Monitor archived. Observations stay available.',
        );
      })
      .catch((error: unknown) => {
        if (isAbortError(error)) return;
        setLifecyclePending(null);
        setLifecycleNotice(describeApiError(error));
        if (error instanceof ApiRequestError && error.code === 'archived') reload();
      });
  };

  if (load.name === 'loading') {
    return <p role="status">Loading monitor…</p>;
  }

  if (load.name === 'not-found') {
    return (
      <section aria-labelledby="monitor-not-found-heading">
        <h2 id="monitor-not-found-heading">Monitor not found</h2>
        <p>No monitor exists at this address.</p>
        <p>
          <Link to="/">Back to monitors</Link>
        </p>
      </section>
    );
  }

  if (load.name === 'error') {
    return (
      <section aria-labelledby="monitor-error-heading">
        <h2 id="monitor-error-heading">Monitor unavailable</h2>
        <p role="alert">{load.message}</p>
        <button type="button" className="button" onClick={reload}>
          Try again
        </button>
        <p>
          <Link to="/">Back to monitors</Link>
        </p>
      </section>
    );
  }

  const { monitor, observations } = load;
  const archived = monitor.lifecycle === 'archived';
  const newestObservation = observations.length === 0 ? null : observations[0];
  const driftNote =
    newestObservation !== null && monitor.configVersion > newestObservation.configVersion
      ? `Configuration changed since the last check (v${newestObservation.configVersion} → v${monitor.configVersion}).`
      : null;

  return (
    <article className="monitor-detail" aria-labelledby="monitor-detail-heading">
      <h2 id="monitor-detail-heading">{monitor.name}</h2>
      <p className="monitor-meta">
        <LifecycleBadge lifecycle={monitor.lifecycle} />
        <span className="version">v{monitor.configVersion}</span>
      </p>
      <MonitorHeadline lastObservation={newestObservation} now={now} />
      {archived ? (
        <p className="note">Archived monitors are read-only. Observations stay available.</p>
      ) : null}
      <p>
        <Link to="/">Back to monitors</Link>
      </p>

      <section className="panel" aria-labelledby="monitor-configuration-heading">
        <h3 id="monitor-configuration-heading">Configuration</h3>
        <dl className="config-list">
          <div>
            <dt>URL</dt>
            <dd>{monitor.check.url}</dd>
          </div>
          <div>
            <dt>Method</dt>
            <dd>{monitor.check.method}</dd>
          </div>
          <div>
            <dt>Expected status</dt>
            <dd>{monitor.check.expectedStatus}</dd>
          </div>
          <div>
            <dt>Deadline</dt>
            <dd>{formatDeadlineSeconds(monitor.check.deadlineMs)}</dd>
          </div>
          <div>
            <dt>Body limit</dt>
            <dd>{monitor.check.maxBodyBytes} bytes</dd>
          </div>
          <div>
            <dt>Version</dt>
            <dd>v{monitor.configVersion}</dd>
          </div>
          <div>
            <dt>Created</dt>
            <dd>
              <time dateTime={monitor.createdAt}>
                {formatLocalWithOffset(new Date(monitor.createdAt))}
              </time>
            </dd>
          </div>
          <div>
            <dt>Updated</dt>
            <dd>
              <time dateTime={monitor.updatedAt}>
                {formatLocalWithOffset(new Date(monitor.updatedAt))}
              </time>
            </dd>
          </div>
          {monitor.archivedAt === undefined ? null : (
            <div>
              <dt>Archived</dt>
              <dd>
                <time dateTime={monitor.archivedAt}>
                  {formatLocalWithOffset(new Date(monitor.archivedAt))}
                </time>
              </dd>
            </div>
          )}
        </dl>
        {fields === null ? null : (
          <MonitorForm
            fields={fields}
            onFieldsChange={setFields}
            fieldErrors={fieldErrors}
            formMessage={
              editNotice !== null && editNotice.kind === 'message' ? editNotice.text : null
            }
            pending={savePending}
            disabled={archived}
            submitLabel="Save changes"
            onSubmit={saveConfiguration}
          />
        )}
        {editNotice === null || editNotice.kind === 'message' ? null : editNotice.kind ===
          'saved' ? (
          <p className="notice" role="status">
            Changes saved.
          </p>
        ) : (
          <p className="notice" role="alert">
            This monitor changed; review and try again (now v{monitor.configVersion}).
          </p>
        )}
      </section>

      <section className="panel" aria-labelledby="monitor-lifecycle-heading">
        <h3 id="monitor-lifecycle-heading">Lifecycle</h3>
        {archived ? (
          <p className="note">Archiving is final; this monitor accepts no further changes.</p>
        ) : (
          <div className="actions">
            {monitor.lifecycle === 'active' ? (
              <button
                type="button"
                className="button"
                onClick={() => applyLifecycle('pause')}
                disabled={lifecyclePending !== null}
              >
                Pause
              </button>
            ) : (
              <button
                type="button"
                className="button"
                onClick={() => applyLifecycle('resume')}
                disabled={lifecyclePending !== null}
              >
                Resume
              </button>
            )}
            {archiveConfirm ? (
              <span className="confirm-step">
                <span>Archive this monitor? Archiving is final and keeps the observations.</span>
                <button
                  type="button"
                  className="button"
                  onClick={() => applyLifecycle('archive')}
                  disabled={lifecyclePending !== null}
                  autoFocus
                >
                  Confirm archive
                </button>
                <button
                  type="button"
                  className="button"
                  onClick={() => setArchiveConfirm(false)}
                  disabled={lifecyclePending !== null}
                >
                  Cancel
                </button>
              </span>
            ) : (
              <button
                type="button"
                className="button"
                onClick={() => setArchiveConfirm(true)}
                disabled={lifecyclePending !== null}
              >
                Archive
              </button>
            )}
          </div>
        )}
        {lifecycleNotice === null ? null : (
          <p className="notice" role="status">
            {lifecycleNotice}
          </p>
        )}
      </section>

      <section className="panel" aria-labelledby="monitor-check-heading">
        <h3 id="monitor-check-heading">Manual check</h3>
        <button
          type="button"
          className="button"
          onClick={startCheckIfIdle}
          disabled={archived}
          aria-disabled={runState.name === 'running' ? true : undefined}
        >
          {runState.name === 'running'
            ? `Checking… up to ${formatDeadlineSeconds(monitor.check.deadlineMs)}`
            : 'Run check now'}
        </button>
        <p className="note">
          The check runs once, now, and is stored as a manual observation. There is no scheduler in
          M1.
        </p>
        {runState.name === 'notice' ? (
          <div className="notice">
            <p role="status">{runState.message}</p>
            {runState.offerReload === true ? (
              <button
                type="button"
                className="button"
                onClick={() => {
                  setRunState({ name: 'idle' });
                  reload();
                }}
              >
                Reload
              </button>
            ) : null}
          </div>
        ) : null}
      </section>

      <section className="panel" aria-labelledby="monitor-observations-heading">
        <h3 id="monitor-observations-heading">Observations</h3>
        {driftNote === null ? null : <p className="drift-note">{driftNote}</p>}
        <ObservationsTable observations={observations} />
      </section>
    </article>
  );
}

export default MonitorDetailPage;
