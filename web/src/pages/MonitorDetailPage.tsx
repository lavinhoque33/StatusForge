import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiRequestError, ApiValidationError, isAbortError, type FieldIssue } from '../api/http';
import {
  OBSERVATION_LIMIT,
  changeLifecycle,
  getMonitor,
  listGaps,
  listIntervals,
  listObservations,
  runCheck,
  updateMonitor,
  type Gap,
  type Intervals,
  type LifecycleAction,
  type MonitorRecord,
  type Observation,
} from '../api/monitors';
import { listMaintenance, type Window } from '../api/maintenance';
import { incidentLink, listMonitorIncidents, type Incident } from '../api/incidents';
import { LifecycleBadge } from '../components/LifecycleBadge';
import { MonitorForm } from '../components/MonitorForm';
import { MonitorHeadline } from '../components/MonitorHeadline';
import { TimelineTable } from '../components/TimelineTable';
import { HeartbeatTokenSection } from '../components/HeartbeatTokenSection';
import { MonitorApplicationSection } from '../components/MonitorApplicationSection';
import { MaintenanceSection } from '../components/MaintenanceSection';
import { describeApiError } from '../lib/errors';
import {
  MONITOR_FIELD_PATHS,
  checkInputFromFields,
  incidentPolicyFromFields,
  monitorFormFields,
  splitFieldErrors,
  type MonitorFormFields,
} from '../lib/monitorForm';
import { notUpdatedText, updatedText } from '../lib/refresh';
import { intervalChoice, type IntervalChoice } from '../lib/intervalChoice';
import { intervalLabel } from '../lib/intervals';
import { formatDeadlineSeconds, formatLocalWithOffset } from '../lib/time';
import { usePolling } from '../lib/usePolling';
import { FRESHNESS_REFRESH_MS, useNow } from '../lib/useNow';
import { heartbeatDeadlines } from '../lib/heartbeatHeadline';
import { Link } from '../router/Link';

type LoadState =
  | { name: 'loading' }
  | {
      name: 'ready';
      monitor: MonitorRecord;
      observations: Observation[];
      gaps: Gap[];
      incidents: Incident[];
      windows: Window[];
    }
  | { name: 'not-found' }
  | { name: 'error'; message: string };

type RunState =
  | { name: 'idle' }
  | { name: 'running' }
  | { name: 'notice'; message: string; offerReload?: boolean };

type EditNotice = { kind: 'saved' } | { kind: 'conflict' } | { kind: 'message'; text: string };

type Freshness =
  | { name: 'none' }
  | { name: 'updated'; at: number }
  | { name: 'failed'; reason: string; at: number };

/** True when the form fields match what the monitor stores (nothing unsaved). */
function fieldsEqual(fields: MonitorFormFields, stored: MonitorFormFields): boolean {
  return Object.keys(stored).every(
    (key) => fields[key as keyof MonitorFormFields] === stored[key as keyof MonitorFormFields],
  );
}

/**
 * The interval to send on save: the selector's choice, or nothing when the
 * selector was never touched (PATCH omits `intervalSeconds` then, so the
 * backend keeps the stored interval).
 */
function selectedIntervalForMonitor(choice: IntervalChoice): number | undefined {
  return choice.current ?? undefined;
}

/**
 * Detail page: configuration (with its version), lifecycle actions, one manual
 * check, and the merged observation/gap timeline.
 *
 * Mutations are one request each; a `409` is surfaced with its contract message
 * and the monitor is re-read, never retried automatically. The 15 s poll
 * refreshes only the read-only data and pauses while the edit form holds
 * unsaved changes, so a poll can never clobber what the user is typing.
 */
export function MonitorDetailPage({ monitorId }: { monitorId: string }) {
  const [load, setLoad] = useState<LoadState>({ name: 'loading' });
  const [freshness, setFreshness] = useState<Freshness>({ name: 'none' });
  const [reloadToken, setReloadToken] = useState(0);
  const [fields, setFields] = useState<MonitorFormFields | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, FieldIssue>>({});
  const [editNotice, setEditNotice] = useState<EditNotice | null>(null);
  const [savePending, setSavePending] = useState(false);
  const [runState, setRunState] = useState<RunState>({ name: 'idle' });
  const [lifecycleNotice, setLifecycleNotice] = useState<string | null>(null);
  const [lifecyclePending, setLifecyclePending] = useState<LifecycleAction | null>(null);
  const [archiveConfirm, setArchiveConfirm] = useState(false);
  const [intervals, setIntervals] = useState<Intervals | null>(null);
  const choice = useRef<IntervalChoice>(intervalChoice());
  const now = useNow(
    FRESHNESS_REFRESH_MS,
    load.name === 'ready'
      ? load.monitor.kind === 'heartbeat'
        ? heartbeatDeadlines(load.monitor)
        : ['healthy', 'failing', 'checker_problem'].includes(load.monitor.status.state) &&
            load.monitor.status.freshUntil !== null
          ? Date.parse(load.monitor.status.freshUntil)
          : null
      : null,
  );
  const checkController = useRef<AbortController | null>(null);
  const loadRef = useRef(load);
  const fieldsRef = useRef(fields);
  // Focus sinks for lifecycle transitions (D8): after Pause/Resume, and after
  // cancelling the archive confirm, focus lands on the replacement control.
  // The focus call must run after React commits the swapped buttons, so the
  // pending action is stored and applied in an effect. The effect is
  // idempotent and retries until it lands: a racing poll re-render can commit
  // a new button node between the state commit and the passive-effect pass,
  // in which case the next flush focuses the fresh node.
  const [focusTarget, setFocusTarget] = useState<'pause' | 'resume' | 'archive' | null>(null);
  const [focusSeq, setFocusSeq] = useState(0);
  const resumeButtonRef = useRef<HTMLButtonElement | null>(null);
  const pauseButtonRef = useRef<HTMLButtonElement | null>(null);
  const archiveButtonRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    if (focusTarget === null) return;
    const target =
      focusTarget === 'pause'
        ? pauseButtonRef.current
        : focusTarget === 'resume'
          ? resumeButtonRef.current
          : archiveButtonRef.current;
    if (target === null || document.activeElement === target) {
      if (target !== null) setFocusTarget(null);
      return;
    }
    target.focus();
    if (document.activeElement !== target) {
      // The node was replaced mid-flush; run once more after it settles.
      const timer = window.setTimeout(() => setFocusSeq((seq) => seq + 1), 0);
      return () => window.clearTimeout(timer);
    }
    setFocusTarget(null);
  }, [focusSeq, focusTarget]);
  const requestFocus = (target: 'pause' | 'resume' | 'archive') => {
    setFocusTarget(target);
    setFocusSeq((seq) => seq + 1);
  };

  const fetchData = useCallback(
    async (signal: AbortSignal, options: { silent: boolean }) => {
      try {
        const [monitor, observations, gaps, incidents, windows] = await Promise.all([
          getMonitor(monitorId, signal),
          listObservations(monitorId, OBSERVATION_LIMIT, signal),
          listGaps(monitorId, OBSERVATION_LIMIT, signal),
          listMonitorIncidents(monitorId, 5, signal),
          listMaintenance(monitorId, signal),
        ]);
        if (signal.aborted) return;
        // A silent poll never touches the form; while the form holds unsaved
        // changes the fresh monitor only replaces the read-only data. The
        // dirty check compares the latest typed fields with the monitor the
        // page last showed (loadRef), not the one just fetched.
        const storedFields =
          fieldsRef.current !== null && loadRef.current.name === 'ready'
            ? monitorFormFields(loadRef.current.monitor)
            : null;
        const dirtyNow = storedFields !== null && !fieldsEqual(fieldsRef.current!, storedFields);
        if (!options.silent && !dirtyNow) {
          setFields((existing) => existing ?? monitorFormFields(monitor));
        }
        setLoad({ name: 'ready', monitor, observations, gaps, incidents, windows });
        setFreshness({ name: 'updated', at: Date.now() });
      } catch (error: unknown) {
        if (signal.aborted || isAbortError(error)) return;
        if (error instanceof ApiRequestError && error.code === 'monitor_not_found') {
          setLoad({ name: 'not-found' });
          return;
        }
        if (options.silent && loadRef.current.name === 'ready') {
          // A failed silent poll keeps the last data and marks it not updated
          // instead of tearing the page down to the error state.
          setFreshness({ name: 'failed', reason: describeApiError(error), at: Date.now() });
          return;
        }
        setLoad({ name: 'error', message: describeApiError(error) });
      }
    },
    [monitorId],
  );

  useEffect(() => {
    loadRef.current = load;
    fieldsRef.current = fields;
  }, [load, fields]);

  useEffect(() => {
    const controller = new AbortController();
    // Deferred to a microtask so the effect body itself performs no
    // synchronous state updates; the fetch sets state after its await.
    Promise.resolve().then(() => {
      if (!controller.signal.aborted) fetchData(controller.signal, { silent: false });
    });
    return () => controller.abort();
  }, [fetchData, reloadToken]);

  useEffect(() => () => checkController.current?.abort(), []);

  useEffect(() => {
    const controller = new AbortController();
    listIntervals(controller.signal)
      .then((loaded) => {
        if (!controller.signal.aborted) setIntervals(loaded);
      })
      .catch(() => {
        // The selector stays empty; saving keeps the stored interval.
      });
    return () => controller.abort();
  }, []);

  // While the edit form holds unsaved changes a poll must not touch the form
  // fields (the stored name/url differ from what the user typed), so silent
  // polls only refresh the read-only data; the form is left as-is.
  const poll = useCallback(async () => {
    await fetchData(new AbortController().signal, { silent: true });
  }, [fetchData]);

  usePolling({ refresh: poll });

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
        // The headline follows the presented status, which only a poll
        // recomputes; refresh immediately instead of waiting up to 15 s.
        void fetchData(new AbortController().signal, { silent: true });
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

    updateMonitor(
      monitor.id,
      monitor.kind === 'heartbeat'
        ? {
            expectedConfigVersion: monitor.configVersion,
            name: fields.name,
            heartbeat: {
              intervalSeconds: fields.heartbeatIntervalSeconds,
              graceSeconds: fields.heartbeatGraceSeconds,
            },
            incidentPolicy: incidentPolicyFromFields(fields),
          }
        : {
            expectedConfigVersion: monitor.configVersion,
            name: fields.name,
            check: checkInputFromFields(fields),
            intervalSeconds: selectedIntervalForMonitor(choice.current),
            incidentPolicy: incidentPolicyFromFields(fields),
          },
    )
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
        // Keyboard users keep a stable anchor: focus the control that
        // replaced the one they activated (applied after React commits).
        if (action === 'pause') requestFocus('resume');
        else if (action === 'resume') requestFocus('pause');
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

  const { monitor, observations, gaps, incidents, windows } = load;
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
        {monitor.kind === 'heartbeat' ? <span className="type-label">Heartbeat</span> : null}
        <span className="version">v{monitor.configVersion}</span>
      </p>
      <MonitorHeadline
        status={monitor.status}
        intervalSeconds={monitor.intervalSeconds ?? monitor.heartbeat!.intervalSeconds}
        monitor={monitor}
        now={now}
        maintenance={monitor.maintenance}
      />
      {monitor.openIncident === null ? null : (
        <p className="panel incident-alert">
          Open incident since{' '}
          <time dateTime={monitor.openIncident.openedAt}>
            {formatLocalWithOffset(new Date(monitor.openIncident.openedAt))}
          </time>{' '}
          <Link to={incidentLink(monitor.id, monitor.openIncident.id)}>View incident</Link>
        </p>
      )}
      <p className="updated-at" role="status">
        {freshness.name === 'failed'
          ? notUpdatedText(freshness.reason)
          : freshness.name === 'updated'
            ? updatedText(freshness.at, now)
            : null}
      </p>
      {archived ? (
        <p className="note">Archived monitors are read-only. Observations stay available.</p>
      ) : null}
      <p>
        <Link to="/">Back to monitors</Link>
      </p>

      <MonitorApplicationSection
        monitor={monitor}
        onChange={(updated) =>
          setLoad((current) =>
            current.name === 'ready' ? { ...current, monitor: updated } : current,
          )
        }
      />
      <section className="panel" aria-labelledby="monitor-configuration-heading">
        <h3 id="monitor-configuration-heading">Configuration</h3>
        <dl className="config-list">
          {monitor.kind === 'heartbeat' && monitor.heartbeat ? (
            <>
              <div>
                <dt>Interval</dt>
                <dd>{intervalLabel(monitor.heartbeat.intervalSeconds)}</dd>
              </div>
              <div>
                <dt>Grace period</dt>
                <dd>{intervalLabel(monitor.heartbeat.graceSeconds)}</dd>
              </div>
              {monitor.expectation ? (
                <>
                  <div>
                    <dt>Next due</dt>
                    <dd>
                      <time dateTime={monitor.expectation.dueAt}>
                        {formatLocalWithOffset(new Date(monitor.expectation.dueAt))}
                      </time>
                    </dd>
                  </div>
                  <div>
                    <dt>Missing after</dt>
                    <dd>
                      <time dateTime={monitor.expectation.missingAt}>
                        {formatLocalWithOffset(new Date(monitor.expectation.missingAt))}
                      </time>
                    </dd>
                  </div>
                </>
              ) : null}
            </>
          ) : monitor.check ? (
            <>
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
                <dt>Interval</dt>
                <dd>{intervalLabel(monitor.intervalSeconds!)}</dd>
              </div>
            </>
          ) : null}
          <div>
            <dt>Open after</dt>
            <dd>{monitor.incidentPolicy.openAfter} failed checks</dd>
          </div>
          <div>
            <dt>Resolve after</dt>
            <dd>{monitor.incidentPolicy.recoverAfter} healthy checks</dd>
          </div>
          {monitor.check ? (
            <div>
              <dt>Body limit</dt>
              <dd>{monitor.check.maxBodyBytes} bytes</dd>
            </div>
          ) : null}
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
            intervals={intervals}
            storedIntervalSeconds={monitor.intervalSeconds ?? undefined}
            onIntervalChange={(seconds) => {
              choice.current.current = seconds;
            }}
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

      {monitor.heartbeat ? (
        <>
          <HeartbeatTokenSection
            id={monitor.id}
            heartbeat={monitor.heartbeat}
            archived={archived}
            refresh={reload}
          />
          <section className="panel" aria-labelledby="heartbeat-proof-heading">
            <h3 id="heartbeat-proof-heading">What a heartbeat proves</h3>
            <p>
              A request carrying the heartbeat’s token reached StatusForge at the recorded time with
              the recorded fields. It does not prove the job did its work correctly or that its
              output (for example a backup) is usable. A missing report means none was received by
              the deadline while StatusForge was receiving; it does not prove the job did not run.
            </p>
            <p>
              Outages shorter than three times the liveness interval are not detected; a missed run
              during an outage is reported only at the next window.
            </p>
          </section>
        </>
      ) : null}
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
                ref={pauseButtonRef}
              >
                Pause
              </button>
            ) : (
              <button
                type="button"
                className="button"
                onClick={() => applyLifecycle('resume')}
                disabled={lifecyclePending !== null}
                ref={resumeButtonRef}
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
                  onClick={() => {
                    setArchiveConfirm(false);
                    // Back where the confirm flow started (after commit).
                    requestFocus('archive');
                  }}
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
                ref={archiveButtonRef}
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

      <MaintenanceSection
        monitorId={monitor.id}
        archived={archived}
        windows={windows}
        refresh={reload}
      />
      {monitor.kind === 'http' && monitor.check ? (
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
            The check runs once, now, and is stored as a manual observation. Scheduled checks run on
            their own; this never changes the schedule.
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
      ) : null}

      <section className="panel" aria-labelledby="monitor-incidents-heading">
        <h3 id="monitor-incidents-heading">Recent incidents</h3>
        {incidents.length === 0 ? (
          <p>No incidents recorded for this monitor.</p>
        ) : (
          <ul>
            {incidents.map((incident) => (
              <li key={incident.id}>
                <Link to={incidentLink(monitor.id, incident.id)}>
                  {incident.state === 'open' ? 'Open' : 'Resolved'} incident
                </Link>{' '}
                since{' '}
                <time dateTime={incident.openedAt}>
                  {formatLocalWithOffset(new Date(incident.openedAt))}
                </time>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section className="panel" aria-labelledby="monitor-timeline-heading">
        <h3 id="monitor-timeline-heading">
          {monitor.kind === 'heartbeat' ? 'Reports and gaps' : 'Checks and gaps'}
        </h3>
        {driftNote === null ? null : <p className="drift-note">{driftNote}</p>}
        <TimelineTable
          observations={observations}
          gaps={gaps}
          windows={windows}
          monitorKind={monitor.kind}
        />
      </section>
    </article>
  );
}

export default MonitorDetailPage;
