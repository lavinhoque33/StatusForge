/**
 * Typed client for the monitor routes.
 *
 * Responses are validated property by property before they reach the interface:
 * an unknown lifecycle, outcome, or presented status state is a protocol
 * violation and is reported as an unexpected response rather than guessed at.
 * Reason codes stay strings so a vocabulary addition cannot break rendering;
 * the presentation layer maps known codes to words.
 */
import { ApiInvalidResponseError, requestJson, requestNoContent } from './http';
import { parseWindow, type Window } from './maintenance';
import { parseDeletion, type DeletionStatus } from './deletion';

/** Observation window requested by the detail page (contract default is 50). */
export const OBSERVATION_LIMIT = 50;

/** Gap window requested by the detail page. */
export const GAP_LIMIT = 50;

const MONITORS_PATH = '/api/monitors';

export type Lifecycle = 'active' | 'paused' | 'archived';

export type CheckOutcome = 'healthy' | 'failing' | 'checker_problem';

/** The presented status states, including heartbeat lateness. */
export type StatusState =
  'healthy' | 'late' | 'failing' | 'checker_problem' | 'stale' | 'unknown' | 'paused' | 'archived';

/**
 * The state the interface presents: the backend state, with a local switch to
 * `stale` when `freshUntil` has passed.
 */
export type MonitorStatusEffective = StatusState;

/** Why a monitor has no current status. */
export type StatusUnknownReason =
  'no_checks' | 'config_changed' | 'waiting_for_first_report' | 'missing' | 'reported_failure';

/** The status computed on every read, with the observation it is based on. */
export type MonitorStatus = {
  state: StatusState;
  reason: StatusUnknownReason | null;
  observation: Observation | null;
  /** The instant after which the web presents the monitor as stale. */
  freshUntil: string | null;
  evaluatedAt: string;
};

/** The request settings stored on a monitor and copied onto an observation. */
export type CheckConfig = {
  url: string;
  method: string;
  expectedStatus: number;
  deadlineMs: number;
  maxBodyBytes: number;
};
export type IncidentPolicy = { openAfter: number; recoverAfter: number };

export type HeartbeatConfig = {
  intervalSeconds: number;
  graceSeconds: number;
  token: { hint: string; createdAt: string } | null;
  lastReportAt: string | null;
  ingestPath: string;
};
export type Expectation = { dueAt: string; lateAt: string; missingAt: string; staleAt: string };
export type MonitorKind = 'http' | 'heartbeat';
export type Monitor = {
  id: string;
  applicationId: string | null;
  name: string;
  lifecycle: Lifecycle;
  configVersion: number;
  /** Seconds between scheduled checks. */
  intervalSeconds: number | null;
  kind: MonitorKind;
  heartbeat: HeartbeatConfig | null;
  expectation: Expectation | null;
  check: CheckConfig | null;
  incidentPolicy: IncidentPolicy;
  openIncident: { id: string; openedAt: string } | null;
  maintenance: { active: Window | null; next: Window | null };
  createdAt: string;
  updatedAt: string;
  pausedAt?: string;
  archivedAt?: string;
  deletion: DeletionStatus | null;
};

export type Observation = {
  kind: 'http_check' | 'heartbeat_report' | 'heartbeat_missed';
  report: {
    runId: string | null;
    finishedAt: string | null;
    durationMs: number | null;
    exitCode: number | null;
    message: string | null;
    late: boolean;
  } | null;
  id: string;
  monitorId: string;
  configVersion: number;
  initiatedBy: string;
  /** Why the check ran; null for manual checks. */
  trigger: string | null;
  /** The work item's due time; null for manual checks. */
  dueAt: string | null;
  /** Whether this observation updated current status (ADR 0003 D5). */
  counted: boolean;
  notCountedReason: string | null;
  maintenanceWindowId: string | null;
  request: CheckConfig | null;
  startedAt: string;
  completedAt: string;
  durationMs: number;
  outcome: CheckOutcome;
  reason: string;
  observedStatus?: number;
  bodyBytesRead: number;
  bodyTruncated: boolean;
};

/** A range of missed schedule slots. */
export type Gap = {
  id: string;
  monitorId: string;
  fromDueAt: string;
  toDueAt: string;
  missedCount: number;
  reason: string;
  recordedAt: string;
};

/** The intervals this backend process offers. */
export type Intervals = {
  intervalSeconds: number[];
  defaultIntervalSeconds: number;
  heartbeat: {
    intervalSeconds: number[];
    graceSeconds: number[];
    defaultIntervalSeconds: number;
    defaultGraceSeconds: number;
  };
};

/**
 * A `Monitor` response: every monitor payload carries its presented status
 *, in the list and on its own.
 */
export type MonitorRecord = Monitor & { status: MonitorStatus };

export type CheckInput = {
  url: string;
  expectedStatus: number;
  deadlineMs: number;
};

export type CreateMonitorInput = {
  name: string;
  kind?: MonitorKind;
  check?: CheckInput;
  intervalSeconds?: number;
  heartbeat?: { intervalSeconds: number; graceSeconds: number };
  incidentPolicy?: IncidentPolicy;
};
export type CreatedMonitor = MonitorRecord & { issuedToken?: string };

export type UpdateMonitorInput = {
  expectedConfigVersion: number;
  name: string;
  check?: CheckInput;
  intervalSeconds?: number;
  heartbeat?: { intervalSeconds: number; graceSeconds: number };
  incidentPolicy?: IncidentPolicy;
};

export type LifecycleAction = 'pause' | 'resume' | 'archive';

function invalid(): never {
  throw new ApiInvalidResponseError();
}

/** `value.prop` on a narrowed `object` loses the key; a cast keeps it. */
function prop(value: object, key: string): unknown {
  return (value as Record<string, unknown>)[key];
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value !== '';
}

function isTimestamp(value: unknown): value is string {
  return typeof value === 'string' && !Number.isNaN(Date.parse(value));
}

function isInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value);
}

function isLifecycle(value: unknown): value is Lifecycle {
  return value === 'active' || value === 'paused' || value === 'archived';
}

function isMonitorKind(value: unknown): value is MonitorKind {
  return value === 'http' || value === 'heartbeat';
}
function isOutcome(value: unknown): value is CheckOutcome {
  return value === 'healthy' || value === 'failing' || value === 'checker_problem';
}

function isStatusState(value: unknown): value is StatusState {
  return (
    value === 'healthy' ||
    value === 'failing' ||
    value === 'late' ||
    value === 'checker_problem' ||
    value === 'stale' ||
    value === 'unknown' ||
    value === 'paused' ||
    value === 'archived'
  );
}

function isStatusUnknownReason(value: unknown): value is StatusUnknownReason {
  return (
    value === 'no_checks' ||
    value === 'config_changed' ||
    value === 'waiting_for_first_report' ||
    value === 'missing' ||
    value === 'reported_failure'
  );
}

/** Parse the `check` object of a monitor or the `request` object of an observation. */
export function parseCheckConfig(value: unknown): CheckConfig {
  if (typeof value !== 'object' || value === null) invalid();
  if (!('url' in value) || !isNonEmptyString(value.url)) invalid();
  if (!('method' in value) || !isNonEmptyString(value.method)) invalid();
  if (!('expectedStatus' in value) || !isInteger(value.expectedStatus)) invalid();
  if (!('deadlineMs' in value) || !isInteger(value.deadlineMs) || value.deadlineMs < 1) invalid();
  if (!('maxBodyBytes' in value) || !isInteger(value.maxBodyBytes) || value.maxBodyBytes < 1) {
    invalid();
  }
  return {
    url: value.url,
    method: value.method,
    expectedStatus: value.expectedStatus,
    deadlineMs: value.deadlineMs,
    maxBodyBytes: value.maxBodyBytes,
  };
}

/** Parse a `Monitor` payload; rejects unknown lifecycles and missing fields. */
export function parseMonitor(value: unknown): Monitor {
  if (typeof value !== 'object' || value === null) invalid();
  if (!('id' in value) || !isNonEmptyString(value.id)) invalid();
  if (
    !('applicationId' in value) ||
    (value.applicationId !== null && !isNonEmptyString(value.applicationId))
  )
    invalid();
  if (!('name' in value) || typeof value.name !== 'string') invalid();
  if (!('lifecycle' in value) || !isLifecycle(value.lifecycle)) invalid();
  if (!('configVersion' in value) || !isInteger(value.configVersion) || value.configVersion < 1) {
    invalid();
  }
  const kind = 'kind' in value ? value.kind : 'http';
  if (!isMonitorKind(kind)) invalid();
  if (
    !('intervalSeconds' in value) ||
    (kind === 'http'
      ? !isInteger(value.intervalSeconds) || value.intervalSeconds < 1
      : value.intervalSeconds !== null)
  )
    invalid();
  if (!('check' in value)) invalid();
  if (kind === 'http' && value.check === null) invalid();
  if (kind === 'heartbeat' && value.check !== null) invalid();
  let heartbeat: HeartbeatConfig | null = null;
  let expectation: Expectation | null = null;
  if (kind === 'heartbeat') {
    const hb = prop(value, 'heartbeat');
    if (typeof hb !== 'object' || hb === null) invalid();
    if (
      !isInteger(prop(hb, 'intervalSeconds')) ||
      !isInteger(prop(hb, 'graceSeconds')) ||
      (prop(hb, 'intervalSeconds') as number) < 1 ||
      (prop(hb, 'graceSeconds') as number) < 1 ||
      (prop(hb, 'graceSeconds') as number) > (prop(hb, 'intervalSeconds') as number) ||
      !isNonEmptyString(prop(hb, 'ingestPath'))
    )
      invalid();
    const token = prop(hb, 'token');
    if (
      token !== null &&
      (typeof token !== 'object' ||
        !isNonEmptyString(prop(token as object, 'hint')) ||
        !isTimestamp(prop(token as object, 'createdAt')))
    )
      invalid();
    const lastReportAt = prop(hb, 'lastReportAt');
    if (lastReportAt !== null && !isTimestamp(lastReportAt)) invalid();
    heartbeat = {
      intervalSeconds: prop(hb, 'intervalSeconds') as number,
      graceSeconds: prop(hb, 'graceSeconds') as number,
      ingestPath: prop(hb, 'ingestPath') as string,
      lastReportAt: lastReportAt as string | null,
      token:
        token === null
          ? null
          : {
              hint: prop(token as object, 'hint') as string,
              createdAt: prop(token as object, 'createdAt') as string,
            },
    };
    const exp = prop(value, 'expectation');
    if (exp !== null) {
      if (
        typeof exp !== 'object' ||
        !isTimestamp(prop(exp, 'dueAt')) ||
        !isTimestamp(prop(exp, 'lateAt')) ||
        !isTimestamp(prop(exp, 'missingAt')) ||
        !isTimestamp(prop(exp, 'staleAt'))
      )
        invalid();
      expectation = {
        dueAt: prop(exp, 'dueAt') as string,
        lateAt: prop(exp, 'lateAt') as string,
        missingAt: prop(exp, 'missingAt') as string,
        staleAt: prop(exp, 'staleAt') as string,
      };
    }
  }
  if (!('createdAt' in value) || !isTimestamp(value.createdAt)) invalid();
  if (!('updatedAt' in value) || !isTimestamp(value.updatedAt)) invalid();
  if (
    !('incidentPolicy' in value) ||
    typeof value.incidentPolicy !== 'object' ||
    value.incidentPolicy === null
  )
    invalid();
  const policy = value.incidentPolicy;
  if (
    !('openAfter' in policy) ||
    !isInteger(policy.openAfter) ||
    policy.openAfter < 1 ||
    policy.openAfter > 5 ||
    !('recoverAfter' in policy) ||
    !isInteger(policy.recoverAfter) ||
    policy.recoverAfter < 1 ||
    policy.recoverAfter > 5
  )
    invalid();
  if (!('openIncident' in value)) invalid();
  const openIncident = value.openIncident;
  if (
    openIncident !== null &&
    (typeof openIncident !== 'object' ||
      !('id' in openIncident) ||
      !isNonEmptyString(openIncident.id) ||
      !('openedAt' in openIncident) ||
      !isTimestamp(openIncident.openedAt))
  )
    invalid();

  if (
    !('maintenance' in value) ||
    typeof value.maintenance !== 'object' ||
    value.maintenance === null
  )
    invalid();
  const maintenance = value.maintenance as Record<string, unknown>;
  if (!('active' in maintenance) || !('next' in maintenance)) invalid();
  const monitor: Monitor = {
    id: value.id,
    name: value.name,
    applicationId: value.applicationId as string | null,
    lifecycle: value.lifecycle,
    configVersion: value.configVersion,
    intervalSeconds: kind === 'http' ? (value.intervalSeconds as number) : null,
    kind,
    heartbeat,
    expectation,
    check: kind === 'http' ? parseCheckConfig(value.check) : null,
    incidentPolicy: { openAfter: policy.openAfter, recoverAfter: policy.recoverAfter },
    openIncident:
      openIncident === null
        ? null
        : { id: String(openIncident.id), openedAt: String(openIncident.openedAt) },
    maintenance: {
      active: maintenance.active === null ? null : parseWindow(maintenance.active),
      next: maintenance.next === null ? null : parseWindow(maintenance.next),
    },
    createdAt: value.createdAt,
    updatedAt: value.updatedAt,
    deletion: parseDeletion(prop(value, 'deletion')),
  };
  if ('pausedAt' in value && value.pausedAt !== undefined && value.pausedAt !== null) {
    if (!isTimestamp(value.pausedAt)) invalid();
    monitor.pausedAt = value.pausedAt;
  }
  if ('archivedAt' in value && value.archivedAt !== undefined && value.archivedAt !== null) {
    if (!isTimestamp(value.archivedAt)) invalid();
    monitor.archivedAt = value.archivedAt;
  }
  return monitor;
}

/**
 * Parse a `Monitor` payload; `unknown` is never accepted as an outcome, and an
 * unknown presented status state or unknown reason is a protocol violation
 *, never reinterpreted.
 */
export function parseObservation(value: unknown): Observation {
  if (typeof value !== 'object' || value === null) invalid();
  const kind = 'kind' in value ? value.kind : 'http_check';
  if (kind !== 'http_check' && kind !== 'heartbeat_report' && kind !== 'heartbeat_missed')
    invalid();
  if (!('id' in value) || !isNonEmptyString(value.id)) invalid();
  if (!('monitorId' in value) || !isNonEmptyString(value.monitorId)) invalid();
  if (!('configVersion' in value) || !isInteger(value.configVersion) || value.configVersion < 1) {
    invalid();
  }
  if (!('initiatedBy' in value) || !isNonEmptyString(value.initiatedBy)) invalid();
  if (!('counted' in value) || typeof value.counted !== 'boolean') invalid();
  if (!('request' in value)) invalid();
  if (!('startedAt' in value) || !isTimestamp(value.startedAt)) invalid();
  if (!('completedAt' in value) || !isTimestamp(value.completedAt)) invalid();
  if (!('durationMs' in value) || !isInteger(value.durationMs) || value.durationMs < 0) invalid();
  if (!('outcome' in value) || !isOutcome(value.outcome)) invalid();
  if (!isNonEmptyString(prop(value, 'reason'))) invalid();
  if (
    kind === 'http_check' &&
    (!('bodyBytesRead' in value) ||
      !isInteger(value.bodyBytesRead) ||
      value.bodyBytesRead < 0 ||
      !('bodyTruncated' in value) ||
      typeof value.bodyTruncated !== 'boolean')
  )
    invalid();
  const rawReport = prop(value, 'report');
  let report: Observation['report'] = null;
  if (kind === 'heartbeat_report') {
    if (typeof rawReport !== 'object' || rawReport === null) invalid();
    const optionalString = (key: string) => {
      const item = prop(rawReport, key);
      if (item !== null && typeof item !== 'string') invalid();
      return item as string | null;
    };
    const optionalNumber = (key: string) => {
      const item = prop(rawReport, key);
      if (item !== null && !isInteger(item)) invalid();
      return item as number | null;
    };
    const finishedAt = optionalString('finishedAt');
    if (finishedAt !== null && !isTimestamp(finishedAt)) invalid();
    if (typeof prop(rawReport, 'late') !== 'boolean') invalid();
    report = {
      runId: optionalString('runId'),
      finishedAt,
      durationMs: optionalNumber('durationMs'),
      exitCode: optionalNumber('exitCode'),
      message: optionalString('message'),
      late: prop(rawReport, 'late') as boolean,
    };
  } else if (kind === 'http_check' && rawReport !== undefined && rawReport !== null) invalid();

  let trigger: string | null = null;
  if (prop(value, 'trigger') !== null && prop(value, 'trigger') !== undefined) {
    if (!isNonEmptyString(prop(value, 'trigger'))) invalid();
    trigger = prop(value, 'trigger') as string;
  }
  let dueAt: string | null = null;
  if (prop(value, 'dueAt') !== null && prop(value, 'dueAt') !== undefined) {
    if (!isTimestamp(prop(value, 'dueAt'))) invalid();
    dueAt = prop(value, 'dueAt') as string;
  }
  let notCountedReason: string | null = null;
  if (prop(value, 'notCountedReason') !== null && prop(value, 'notCountedReason') !== undefined) {
    if (!isNonEmptyString(prop(value, 'notCountedReason'))) invalid();
    notCountedReason = prop(value, 'notCountedReason') as string;
  }

  const windowId = prop(value, 'maintenanceWindowId');
  if (windowId !== null && !isNonEmptyString(windowId)) invalid();
  const observation: Observation = {
    id: value.id,
    monitorId: value.monitorId,
    kind,
    report,
    configVersion: value.configVersion,
    initiatedBy: value.initiatedBy,
    trigger,
    dueAt,
    counted: value.counted,
    maintenanceWindowId: windowId,
    notCountedReason,
    request:
      kind === 'http_check'
        ? parseCheckConfig(value.request)
        : value.request === null
          ? null
          : invalid(),
    startedAt: value.startedAt,
    completedAt: value.completedAt,
    durationMs: value.durationMs,
    outcome: value.outcome,
    reason: prop(value, 'reason') as string,
    bodyBytesRead: kind === 'http_check' ? (prop(value, 'bodyBytesRead') as number) : 0,
    bodyTruncated: kind === 'http_check' ? (prop(value, 'bodyTruncated') as boolean) : false,
  };
  if (
    'observedStatus' in value &&
    value.observedStatus !== undefined &&
    value.observedStatus !== null
  ) {
    if (!isInteger(value.observedStatus)) invalid();
    observation.observedStatus = value.observedStatus;
  }
  return observation;
}

/**
 * Parse the presented `status`. The state vocabulary is
 * closed here, like outcomes and lifecycles: an unexpected state or reason is
 * an unexpected response, never a guessed-at status.
 */
export function parseMonitorStatus(value: unknown): MonitorStatus {
  if (typeof value !== 'object' || value === null) invalid();
  if (!('state' in value) || !isStatusState(prop(value, 'state'))) invalid();
  if (!('evaluatedAt' in value) || !isTimestamp(prop(value, 'evaluatedAt'))) invalid();

  let reason: StatusUnknownReason | null = null;
  if (prop(value, 'reason') !== null && prop(value, 'reason') !== undefined) {
    if (!isStatusUnknownReason(prop(value, 'reason'))) invalid();
    reason = prop(value, 'reason') as StatusUnknownReason;
  }
  let observation: Observation | null = null;
  if (prop(value, 'observation') !== null && prop(value, 'observation') !== undefined) {
    observation = parseObservation(prop(value, 'observation'));
  }
  let freshUntil: string | null = null;
  if (prop(value, 'freshUntil') !== null && prop(value, 'freshUntil') !== undefined) {
    if (!isTimestamp(prop(value, 'freshUntil'))) invalid();
    freshUntil = prop(value, 'freshUntil') as string;
  }
  return {
    state: prop(value, 'state') as MonitorStatus['state'],
    reason,
    observation,
    freshUntil,
    evaluatedAt: prop(value, 'evaluatedAt') as string,
  };
}

function parseMonitorRecord(value: unknown): MonitorRecord {
  if (typeof value !== 'object' || value === null) invalid();
  if (!('status' in value)) invalid();
  return { ...parseMonitor(value), status: parseMonitorStatus(prop(value, 'status')) };
}
function parseCreatedMonitor(value: unknown): CreatedMonitor {
  const monitor = parseMonitorRecord(value);
  if (monitor.kind === 'heartbeat') {
    if (!isNonEmptyString(prop(value as object, 'issuedToken'))) invalid();
    return { ...monitor, issuedToken: prop(value as object, 'issuedToken') as string };
  }
  return monitor;
}

function parseMonitorList(body: unknown): MonitorRecord[] {
  if (typeof body !== 'object' || body === null) invalid();
  if (!('monitors' in body) || !Array.isArray(body.monitors)) invalid();
  return body.monitors.map(parseMonitorRecord);
}

function parseObservationList(body: unknown): Observation[] {
  if (typeof body !== 'object' || body === null) invalid();
  if (!('observations' in body) || !Array.isArray(body.observations)) invalid();
  return body.observations.map(parseObservation);
}

export type HistoryPage<T> = {
  items: T[];
  nextCursor: string | null;
  searchedThrough: string | null;
};

export type ObservationFilters = {
  outcome?: string;
  counted?: boolean;
  maintenance?: boolean;
  from?: string;
  to?: string;
};

function pageFields(body: object): Pick<HistoryPage<never>, 'nextCursor' | 'searchedThrough'> {
  const cursor = prop(body, 'nextCursor');
  const searched = prop(body, 'searchedThrough');
  if (cursor !== null && !isNonEmptyString(cursor)) invalid();
  if (searched !== null && !isTimestamp(searched)) invalid();
  return { nextCursor: cursor as string | null, searchedThrough: searched as string | null };
}

export function parseObservationPage(body: unknown): HistoryPage<Observation> {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) invalid();
  return { items: parseObservationList(body), ...pageFields(body) };
}

export function parseGapPage(body: unknown): HistoryPage<Gap> {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) invalid();
  return { items: parseGapList(body), ...pageFields(body) };
}

function parseGap(value: unknown): Gap {
  if (typeof value !== 'object' || value === null) invalid();
  if (!('id' in value) || !isNonEmptyString(value.id)) invalid();
  if (!('monitorId' in value) || !isNonEmptyString(value.monitorId)) invalid();
  if (!('fromDueAt' in value) || !isTimestamp(value.fromDueAt)) invalid();
  if (!('toDueAt' in value) || !isTimestamp(value.toDueAt)) invalid();
  if (!('missedCount' in value) || !isInteger(value.missedCount) || value.missedCount < 1)
    invalid();
  if (!('reason' in value) || !isNonEmptyString(value.reason)) invalid();
  if (!('recordedAt' in value) || !isTimestamp(value.recordedAt)) invalid();
  return {
    id: value.id,
    monitorId: value.monitorId,
    fromDueAt: value.fromDueAt,
    toDueAt: value.toDueAt,
    missedCount: value.missedCount,
    reason: value.reason,
    recordedAt: value.recordedAt,
  };
}

function parseGapList(body: unknown): Gap[] {
  if (typeof body !== 'object' || body === null) invalid();
  if (!('gaps' in body) || !Array.isArray(body.gaps)) invalid();
  return body.gaps.map(parseGap);
}

/** Parse `GET /api/intervals`. */
export function parseIntervals(value: unknown): Intervals {
  if (typeof value !== 'object' || value === null) invalid();
  if (
    !('intervalSeconds' in value) ||
    !Array.isArray(value.intervalSeconds) ||
    value.intervalSeconds.length === 0
  ) {
    invalid();
  }
  const intervalSeconds: number[] = [];
  for (const entry of value.intervalSeconds) {
    if (!isInteger(entry) || entry < 1) invalid();
    intervalSeconds.push(entry);
  }
  if (
    !('defaultIntervalSeconds' in value) ||
    !isInteger(value.defaultIntervalSeconds) ||
    value.defaultIntervalSeconds < 1
  ) {
    invalid();
  }
  const hb = prop(value, 'heartbeat');
  if (typeof hb !== 'object' || hb === null) invalid();
  const list = (key: string) => {
    const values = prop(hb, key);
    if (
      !Array.isArray(values) ||
      values.length === 0 ||
      values.some((entry) => !isInteger(entry) || entry < 1)
    )
      invalid();
    return values as number[];
  };
  const hbInterval = prop(hb, 'defaultIntervalSeconds');
  const hbGrace = prop(hb, 'defaultGraceSeconds');
  if (!isInteger(hbInterval) || hbInterval < 1 || !isInteger(hbGrace) || hbGrace < 1) invalid();
  return {
    intervalSeconds,
    defaultIntervalSeconds: value.defaultIntervalSeconds,
    heartbeat: {
      intervalSeconds: list('intervalSeconds'),
      graceSeconds: list('graceSeconds'),
      defaultIntervalSeconds: hbInterval,
      defaultGraceSeconds: hbGrace,
    },
  };
}

/** `GET /api/monitors` — every lifecycle, oldest first (server order). */
export function listMonitors(signal?: AbortSignal): Promise<MonitorRecord[]> {
  return requestJson(MONITORS_PATH, parseMonitorList, { signal });
}

/** `POST /api/monitors` (201 `Monitor`). */
export function createMonitor(
  input: CreateMonitorInput,
  signal?: AbortSignal,
): Promise<CreatedMonitor> {
  return requestJson(MONITORS_PATH, parseCreatedMonitor, { method: 'POST', body: input, signal });
}

/** `GET /api/monitors/{id}`. */
export function getMonitor(id: string, signal?: AbortSignal): Promise<MonitorRecord> {
  return requestJson(`${MONITORS_PATH}/${encodeURIComponent(id)}`, parseMonitorRecord, { signal });
}

/** `PATCH /api/monitors/{id}` guarded by `expectedConfigVersion`. */
export function updateMonitor(
  id: string,
  input: UpdateMonitorInput,
  signal?: AbortSignal,
): Promise<MonitorRecord> {
  return requestJson(`${MONITORS_PATH}/${encodeURIComponent(id)}`, parseMonitorRecord, {
    method: 'PATCH',
    body: input,
    signal,
  });
}

/** Context-only membership never changes the configuration version. */
export function requestApplicationMembership(
  id: string,
  applicationId: string | null,
): Promise<MonitorRecord> {
  return requestJson(`${MONITORS_PATH}/${encodeURIComponent(id)}/application`, parseMonitorRecord, {
    method: 'PUT',
    body: { applicationId },
  });
}
/** `POST /api/monitors/{id}/lifecycle`. */
export function changeLifecycle(
  id: string,
  action: LifecycleAction,
  signal?: AbortSignal,
): Promise<MonitorRecord> {
  return requestJson(`${MONITORS_PATH}/${encodeURIComponent(id)}/lifecycle`, parseMonitorRecord, {
    method: 'POST',
    body: { action },
    signal,
  });
}

/** `POST /api/monitors/{id}/checks` — runs synchronously, returns the observation. */
export function runCheck(id: string, signal?: AbortSignal): Promise<Observation> {
  return requestJson(`${MONITORS_PATH}/${encodeURIComponent(id)}/checks`, parseObservation, {
    method: 'POST',
    signal,
  });
}

/** `GET /api/monitors/{id}/observations?limit=N` — newest first (server order). */
export function listObservations(
  id: string,
  limit = OBSERVATION_LIMIT,
  signal?: AbortSignal,
): Promise<Observation[]> {
  return requestJson(
    `${MONITORS_PATH}/${encodeURIComponent(id)}/observations?limit=${limit}`,
    parseObservationList,
    { signal },
  );
}

/** `GET /api/monitors/{id}/gaps?limit=N` — newest `fromDueAt` first (server order). */
export function listGaps(id: string, limit = GAP_LIMIT, signal?: AbortSignal): Promise<Gap[]> {
  return requestJson(
    `${MONITORS_PATH}/${encodeURIComponent(id)}/gaps?limit=${limit}`,
    parseGapList,
    { signal },
  );
}

export function getObservationPage(
  id: string,
  limit: number,
  filters: ObservationFilters,
  before?: string,
  signal?: AbortSignal,
): Promise<HistoryPage<Observation>> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (before) params.set('before', before);
  if (filters.outcome) params.set('outcome', filters.outcome);
  if (filters.counted !== undefined) params.set('counted', String(filters.counted));
  if (filters.maintenance !== undefined) params.set('maintenance', String(filters.maintenance));
  if (filters.from) params.set('from', filters.from);
  if (filters.to) params.set('to', filters.to);
  return requestJson(
    `${MONITORS_PATH}/${encodeURIComponent(id)}/observations?${params}`,
    parseObservationPage,
    { signal },
  );
}

export function getGapPage(
  id: string,
  limit: number,
  before?: string,
  signal?: AbortSignal,
): Promise<HistoryPage<Gap>> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (before) params.set('before', before);
  return requestJson(`${MONITORS_PATH}/${encodeURIComponent(id)}/gaps?${params}`, parseGapPage, {
    signal,
  });
}

/** `GET /api/intervals` — the intervals this process offers, ascending. */
export function listIntervals(signal?: AbortSignal): Promise<Intervals> {
  return requestJson('/api/intervals', parseIntervals, { signal });
}

export type IssuedToken = { token: string; hint: string; createdAt: string };
function parseIssuedToken(value: unknown): IssuedToken {
  if (
    typeof value !== 'object' ||
    value === null ||
    !isNonEmptyString(prop(value, 'token')) ||
    !isNonEmptyString(prop(value, 'hint')) ||
    !isTimestamp(prop(value, 'createdAt'))
  )
    invalid();
  return {
    token: prop(value, 'token') as string,
    hint: prop(value, 'hint') as string,
    createdAt: prop(value, 'createdAt') as string,
  };
}
export function rotateHeartbeatToken(id: string): Promise<IssuedToken> {
  return requestJson(
    `${MONITORS_PATH}/${encodeURIComponent(id)}/heartbeat/token`,
    parseIssuedToken,
    { method: 'POST' },
  );
}
export function revokeHeartbeatToken(id: string): Promise<void> {
  return requestNoContent(`${MONITORS_PATH}/${encodeURIComponent(id)}/heartbeat/token`);
}
export type Liveness = { aliveAt: string; outages: { from: string; to: string }[] };
export function getLiveness(signal?: AbortSignal): Promise<Liveness> {
  return requestJson(
    '/api/system/liveness',
    (value: unknown) => {
      if (
        typeof value !== 'object' ||
        value === null ||
        !isTimestamp(prop(value, 'aliveAt')) ||
        !Array.isArray(prop(value, 'outages'))
      )
        invalid();
      return {
        aliveAt: prop(value, 'aliveAt') as string,
        outages: (prop(value, 'outages') as unknown[]).map((entry) => {
          if (
            typeof entry !== 'object' ||
            entry === null ||
            !isTimestamp(prop(entry, 'from')) ||
            !isTimestamp(prop(entry, 'to'))
          )
            invalid();
          return { from: prop(entry, 'from') as string, to: prop(entry, 'to') as string };
        }),
      };
    },
    { signal },
  );
}
