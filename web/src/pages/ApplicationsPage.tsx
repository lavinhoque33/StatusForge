import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import {
  archiveApplication,
  createApplication,
  createMarker,
  listApplications,
  listMarkers,
  renameApplication,
  revokeApplicationToken,
  rotateApplicationToken,
  setMembership,
  type Application,
  type Marker,
  type MarkerInput,
} from '../api/applications';
import { ApiRequestError, ApiValidationError, isAbortError, type FieldIssue } from '../api/http';
import { listMonitors, type MonitorRecord } from '../api/monitors';
import { HeartbeatTokenPanel } from '../components/HeartbeatTokenPanel';
import { PermanentDeletion } from '../components/PermanentDeletion';
import { describeApiError, fieldErrorMessage } from '../lib/errors';
import { heartbeatDeadlines, heartbeatState } from '../lib/heartbeatHeadline';
import { applicationSummary } from '../lib/applicationSummary';
import { formatLocalWithOffset, formatRelativeAge } from '../lib/time';
import { effectiveState } from '../lib/statusHeadline';
import { useNow } from '../lib/useNow';
import { usePolling } from '../lib/usePolling';
import { Link } from '../router/Link';

function ApplicationCard({
  application,
  monitors,
  markerRefresh,
  refresh,
  initialToken,
  clearIssuedToken,
  onDeleted,
}: {
  application: Application;
  monitors: MonitorRecord[];
  markerRefresh: number;
  refresh: () => Promise<void>;
  initialToken: string | null;
  clearIssuedToken: () => void;
  onDeleted: (name: string) => void;
}) {
  const [name, setName] = useState(application.name);
  const [token, setToken] = useState(initialToken);
  const [confirm, setConfirm] = useState<'archive' | 'rotate' | 'revoke' | null>(null);
  const [notice, setNotice] = useState('');
  const [pending, setPending] = useState(false);
  const [member, setMember] = useState('');
  const [markers, setMarkers] = useState<Marker[]>([]);
  const [markerError, setMarkerError] = useState('');
  const [markerFields, setMarkerFields] = useState<Record<string, FieldIssue>>({});
  const [marker, setMarker] = useState<MarkerInput>({
    version: '',
    description: '',
    link: '',
    deployedAt: '',
    deploymentId: '',
  });
  const [markerPending, setMarkerPending] = useState(false);
  const [reload, setReload] = useState(0);
  const rotateRef = useRef<HTMLButtonElement>(null);
  const revokeRef = useRef<HTMLButtonElement>(null);
  const archiveRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (confirm) confirmRef.current?.focus();
  }, [confirm]);
  useEffect(() => {
    const controller = new AbortController();
    void listMarkers(application.id, controller.signal)
      .then((entries) => {
        setMarkers(entries);
        setMarkerError('');
      })
      .catch((error: unknown) => {
        if (!isAbortError(error)) setMarkerError(describeApiError(error));
      });
    return () => controller.abort();
  }, [application.id, reload, markerRefresh]);
  const summary = applicationSummary(application.id, monitors, markers);
  const now = useNow(
    60_000,
    summary.members.flatMap((monitor) =>
      monitor.kind === 'heartbeat'
        ? heartbeatDeadlines(monitor)
        : monitor.status.freshUntil !== null
          ? [Date.parse(monitor.status.freshUntil)]
          : [],
    ),
  );
  const archived = application.archivedAt !== null;
  const deleting = application.deletion !== null;
  const available = monitors.filter(
    (monitor) => monitor.lifecycle !== 'archived' && monitor.applicationId !== application.id,
  );
  const act = async (operation: () => Promise<unknown>, success: string) => {
    const action = confirm;
    let saved = false;
    setPending(true);
    setNotice('');
    try {
      await operation();
      saved = true;
      setNotice(success);
      await refresh();
    } catch (error) {
      if (error instanceof ApiRequestError && error.code === 'archived') {
        setNotice('This monitor or application is archived; the change was not saved.');
        await refresh();
      } else if (error instanceof ApiRequestError && error.code === 'duplicate_name') {
        setNotice('An active application already has this name.');
      } else {
        setNotice(describeApiError(error));
      }
    } finally {
      setPending(false);
      setConfirm(null);
      if (action)
        window.setTimeout(() => {
          (saved && action === 'archive'
            ? headingRef
            : action === 'rotate'
              ? rotateRef
              : action === 'revoke'
                ? revokeRef
                : archiveRef
          ).current?.focus();
        }, 0);
    }
  };
  const submitMarker = async (event: FormEvent) => {
    event.preventDefault();
    setNotice('');
    setMarkerPending(true);
    setMarkerError('');
    setMarkerFields({});
    try {
      const input: MarkerInput = { version: marker.version };
      if (marker.description) input.description = marker.description;
      if (marker.link) input.link = marker.link;
      if (marker.deployedAt) input.deployedAt = new Date(marker.deployedAt).toISOString();
      if (marker.deploymentId) input.deploymentId = marker.deploymentId;
      await createMarker(application.id, input);
      setMarker({ version: '', description: '', link: '', deployedAt: '', deploymentId: '' });
      setReload((n) => n + 1);
      setNotice('Deployment marker recorded.');
    } catch (error) {
      if (error instanceof ApiValidationError) {
        setMarkerFields(error.fields);
        setMarkerError('Some values need attention.');
      } else if (error instanceof ApiRequestError && error.code === 'duplicate_deployment') {
        setMarkerError('This deployment ID was already recorded for this application.');
      } else setMarkerError(describeApiError(error));
    } finally {
      setMarkerPending(false);
    }
  };
  return (
    <article className="panel application-card" aria-labelledby={`application-${application.id}`}>
      <h3 ref={headingRef} tabIndex={-1} id={`application-${application.id}`}>
        {application.name}
        {archived ? ' — Archived' : ''}
        {deleting ? ' — Deleting' : ''}
      </h3>
      <p>
        Created{' '}
        <time dateTime={application.createdAt}>
          {formatLocalWithOffset(new Date(application.createdAt))}
        </time>
      </p>
      <section>
        <h4>Current summary</h4>
        <p>
          {summary.members.length} member monitor{summary.members.length === 1 ? '' : 's'};{' '}
          {summary.openIncidents} open incident{summary.openIncidents === 1 ? '' : 's'}.
        </p>
        {summary.members.length ? (
          <ul>
            {summary.members.map((monitor) => (
              <li key={monitor.id}>
                <Link to={`/monitors/${encodeURIComponent(monitor.id)}`}>{monitor.name}</Link> —{' '}
                {monitor.lifecycle === 'active'
                  ? (monitor.kind === 'heartbeat'
                      ? heartbeatState(monitor, now)
                      : effectiveState(monitor.status, now)
                    ).replaceAll('_', ' ')
                  : monitor.lifecycle}
              </li>
            ))}
          </ul>
        ) : (
          <p>No member monitors.</p>
        )}
        {summary.lastDeployment ? (
          <p>
            Last deployment: {summary.lastDeployment.version} —{' '}
            <time dateTime={summary.lastDeployment.deployedAt ?? summary.lastDeployment.reportedAt}>
              {formatLocalWithOffset(
                new Date(summary.lastDeployment.deployedAt ?? summary.lastDeployment.reportedAt),
              )}
            </time>{' '}
            (
            {formatRelativeAge(
              summary.lastDeployment.deployedAt ?? summary.lastDeployment.reportedAt,
              now,
            )}
            ).
          </p>
        ) : (
          <p>No deployment recorded.</p>
        )}
        <p className="note">
          Showing at most 50 recent deployment markers per application; older markers may not be
          included.
        </p>
      </section>
      {token ? (
        <HeartbeatTokenPanel
          token={token}
          ingestPath=""
          applicationId={application.id}
          onDismiss={() => {
            setToken(null);
            clearIssuedToken();
            window.setTimeout(() => headingRef.current?.focus(), 0);
          }}
        />
      ) : null}
      {archived || deleting ? (
        <p>
          {deleting
            ? 'Deleting. Changes are no longer allowed.'
            : 'Archived. Members and token removed; deployment history retained.'}
        </p>
      ) : (
        <>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              void act(() => renameApplication(application.id, name), 'Application renamed.');
            }}
          >
            <label htmlFor={`name-${application.id}`}>Rename application</label>
            <input
              id={`name-${application.id}`}
              value={name}
              maxLength={100}
              onChange={(event) => setName(event.target.value)}
              required
            />
            <button
              type="submit"
              className="button"
              disabled={pending || name === application.name}
            >
              Save name
            </button>
          </form>
          <section>
            <h4>Token</h4>
            <p>
              {application.token ? (
                <>
                  Hint: …{application.token.hint}; created{' '}
                  <time dateTime={application.token.createdAt}>
                    {formatLocalWithOffset(new Date(application.token.createdAt))}
                  </time>
                </>
              ) : (
                'No active token.'
              )}
            </p>
            <div className="actions">
              <button
                ref={rotateRef}
                type="button"
                className="button"
                disabled={pending}
                onClick={() => setConfirm('rotate')}
              >
                {application.token ? 'Rotate token' : 'Issue token'}
              </button>
              {application.token ? (
                <button
                  ref={revokeRef}
                  type="button"
                  className="button"
                  disabled={pending}
                  onClick={() => setConfirm('revoke')}
                >
                  Revoke token
                </button>
              ) : null}
              <button
                ref={archiveRef}
                type="button"
                className="button"
                disabled={pending}
                onClick={() => setConfirm('archive')}
              >
                Archive application
              </button>
            </div>
            {confirm ? (
              <div className="confirm-step">
                <p>
                  {confirm === 'archive'
                    ? 'Archive this application? Its monitors will be unassigned and its token revoked. Markers stay available.'
                    : confirm === 'rotate'
                      ? 'Replace the current token? The old token stops working immediately.'
                      : 'Revoke this token? Deployment ingest will stop.'}
                </p>
                <button
                  ref={confirmRef}
                  type="button"
                  className="button"
                  disabled={pending}
                  onClick={() =>
                    void act(
                      async () => {
                        if (confirm === 'archive') await archiveApplication(application.id);
                        else if (confirm === 'revoke') await revokeApplicationToken(application.id);
                        if (confirm === 'archive' || confirm === 'revoke') {
                          setToken(null);
                          clearIssuedToken();
                        } else {
                          const issued = await rotateApplicationToken(application.id);
                          setToken(issued.token);
                        }
                      },
                      confirm === 'archive'
                        ? 'Application archived.'
                        : confirm === 'revoke'
                          ? 'Token revoked.'
                          : 'Token rotated.',
                    )
                  }
                >
                  Confirm {confirm}
                </button>
                <button
                  type="button"
                  className="button"
                  onClick={() => {
                    const previous = confirm;
                    setConfirm(null);
                    window.setTimeout(
                      () =>
                        (previous === 'rotate'
                          ? rotateRef
                          : previous === 'revoke'
                            ? revokeRef
                            : archiveRef
                        ).current?.focus(),
                      0,
                    );
                  }}
                >
                  Cancel
                </button>
              </div>
            ) : null}
          </section>
        </>
      )}
      {archived ? (
        <PermanentDeletion
          resource="applications"
          id={application.id}
          name={application.name}
          deletion={application.deletion}
          onStatus={() => void refresh()}
          onComplete={() => onDeleted(application.name)}
        />
      ) : null}
      <p role="status">{notice}</p>
      <section>
        <h4>Members</h4>
        {application.members.length === 0 ? (
          <p>No monitors assigned.</p>
        ) : (
          <ul className="application-members">
            {application.members.map((item) => (
              <li key={item.id}>
                {item.name} ({item.kind}){' '}
                {!archived ? (
                  <button
                    type="button"
                    className="button"
                    disabled={pending}
                    aria-label={`Remove ${item.name} from ${application.name}`}
                    onClick={() => void act(() => setMembership(item.id, null), 'Monitor removed.')}
                  >
                    Remove
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {!archived ? (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              if (member)
                void act(() => setMembership(member, application.id), 'Monitor assigned.');
            }}
          >
            <label htmlFor={`member-${application.id}`}>Add monitor</label>
            <select
              id={`member-${application.id}`}
              value={member}
              onChange={(event) => setMember(event.target.value)}
            >
              <option value="">Choose a monitor</option>
              {available.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                  {item.applicationId ? ' (move from another application)' : ''}
                </option>
              ))}
            </select>
            <button type="submit" className="button" disabled={!member || pending}>
              Add member
            </button>
          </form>
        ) : null}
      </section>
      <section>
        <h4>Deployments</h4>
        {markerError ? <p role="alert">{markerError}</p> : null}
        {!archived ? (
          <form onSubmit={(event) => void submitMarker(event)}>
            {(['version', 'description', 'link', 'deployedAt', 'deploymentId'] as const).map(
              (key) => (
                <div key={key} className="field">
                  <label htmlFor={`${key}-${application.id}`}>
                    {
                      {
                        version: 'Version',
                        description: 'Description',
                        link: 'Link',
                        deployedAt: 'Deployed at',
                        deploymentId: 'Deployment ID',
                      }[key]
                    }
                  </label>
                  <input
                    id={`${key}-${application.id}`}
                    type={key === 'deployedAt' ? 'datetime-local' : key === 'link' ? 'url' : 'text'}
                    value={marker[key] ?? ''}
                    required={key === 'version'}
                    maxLength={key === 'description' || key === 'link' ? 500 : 100}
                    aria-invalid={!!markerFields[key]}
                    aria-describedby={
                      markerFields[key] ? `${key}-${application.id}-error` : undefined
                    }
                    onChange={(event) =>
                      setMarker((previous) => ({ ...previous, [key]: event.target.value }))
                    }
                  />
                  {markerFields[key] ? (
                    <p id={`${key}-${application.id}-error`} role="alert">
                      {fieldErrorMessage(markerFields[key])}
                    </p>
                  ) : null}
                </div>
              ),
            )}
            <button type="submit" className="button" disabled={markerPending}>
              Record deployment
            </button>
          </form>
        ) : null}
        {markers.length === 0 ? (
          <p>No deployment markers recorded.</p>
        ) : (
          <ul className="application-markers">
            {markers.map((entry) => (
              <li key={entry.id} className="panel">
                <strong>{entry.version}</strong> · {entry.source} ·{' '}
                <time dateTime={entry.reportedAt}>
                  {formatLocalWithOffset(new Date(entry.reportedAt))}
                </time>
                {entry.description ? <p>{entry.description}</p> : null}
                {entry.link ? (
                  <p>
                    <a href={entry.link} rel="noopener noreferrer" target="_blank">
                      {entry.link}
                    </a>
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>
    </article>
  );
}

export function ApplicationsPage() {
  const [applications, setApplications] = useState<Application[]>([]);
  const [monitors, setMonitors] = useState<MonitorRecord[]>([]);
  const [issued, setIssued] = useState<Record<string, string>>({});
  const [name, setName] = useState('');
  const [fields, setFields] = useState<Record<string, FieldIssue>>({});
  const [error, setError] = useState('');
  const [creationNotice, setCreationNotice] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [deletionNotice, setDeletionNotice] = useState('');
  const [markerRefresh, setMarkerRefresh] = useState(0);
  const [pending, setPending] = useState(false);
  const refresh = useCallback(async (signal?: AbortSignal) => {
    try {
      const [apps, allMonitors] = await Promise.all([
        listApplications(signal),
        listMonitors(signal),
      ]);
      if (signal?.aborted) return;
      setApplications(apps);
      setMonitors(allMonitors);
      setLoaded(true);
      setMarkerRefresh((value) => value + 1);
      setError('');
    } catch (cause) {
      if (!isAbortError(cause)) setError(describeApiError(cause));
    }
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    Promise.resolve().then(() => {
      if (!controller.signal.aborted) void refresh(controller.signal);
    });
    return () => controller.abort();
  }, [refresh]);
  usePolling({ refresh: () => refresh() });
  const create = async (event: FormEvent) => {
    event.preventDefault();
    setPending(true);
    setError('');
    setCreationNotice('');
    setFields({});
    try {
      const app = await createApplication(name);
      setCreationNotice('Application created. Token shown once.');
      setIssued((current) => ({ ...current, [app.id]: app.issuedToken }));
      setName('');
      await refresh();
    } catch (cause) {
      if (cause instanceof ApiValidationError) setFields(cause.fields);
      setError(
        cause instanceof ApiRequestError && cause.code === 'duplicate_name'
          ? 'An active application already has this name.'
          : describeApiError(cause),
      );
    } finally {
      setPending(false);
    }
  };
  return (
    <section aria-labelledby="applications-heading">
      <h2 id="applications-heading">Applications</h2>
      <p>
        Group monitors and record deployments as context. Markers never change monitoring status.
      </p>
      <form className="panel" onSubmit={(event) => void create(event)}>
        <h3>Create application</h3>
        <label htmlFor="new-application-name">Name</label>
        <input
          id="new-application-name"
          value={name}
          maxLength={100}
          required
          aria-invalid={!!fields.name}
          aria-describedby={fields.name ? 'new-application-name-error' : undefined}
          onChange={(event) => setName(event.target.value)}
        />
        {fields.name ? (
          <p id="new-application-name-error" role="alert">
            {fieldErrorMessage(fields.name)}
          </p>
        ) : null}
        <button type="submit" className="button" disabled={pending}>
          Create application
        </button>
      </form>
      {creationNotice ? <p role="status">{creationNotice}</p> : null}
      {deletionNotice ? <p role="status">{deletionNotice}</p> : null}
      {error ? <p role="alert">{error}</p> : null}
      {!loaded ? (
        <p role="status">Loading applications…</p>
      ) : applications.length === 0 ? (
        <p>No applications yet.</p>
      ) : (
        <div className="applications-list">
          {applications.map((application) => (
            <ApplicationCard
              key={application.id}
              application={application}
              markerRefresh={markerRefresh}
              monitors={monitors}
              initialToken={issued[application.id] ?? null}
              onDeleted={(deletedName) => {
                setDeletionNotice(`${deletedName} was deleted permanently.`);
                void refresh();
              }}
              clearIssuedToken={() =>
                setIssued((current) => {
                  const next = { ...current };
                  delete next[application.id];
                  return next;
                })
              }
              refresh={() => refresh()}
            />
          ))}
        </div>
      )}
    </section>
  );
}
