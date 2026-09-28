/**
 * Typed client for the monitor routes.
 *
 * Responses are validated property by property before they reach the interface:
 * an unknown lifecycle, outcome, or presented status state is a protocol
 * violation and is reported as an unexpected response rather than guessed at.
 * Reason codes stay strings so a vocabulary addition cannot break rendering;
 * the presentation layer maps known codes to words.
 */
import { ApiInvalidResponseError, requestJson } from './http';

/** Observation window requested by the detail page (contract default is 50). */
export const OBSERVATION_LIMIT = 50;

/** Gap window requested by the detail page. */
export const GAP_LIMIT = 50;

const MONITORS_PATH = '/api/monitors';

export type Lifecycle = 'active' | 'paused' | 'archived';

export type CheckOutcome = 'healthy' | 'failing' | 'checker_problem';

/** The presented status states. */
export type StatusState =
  'healthy' | 'failing' | 'checker_problem' | 'stale' | 'unknown' | 'paused' | 'archived';

/**
 * The state the interface presents: the backend state, with a local switch to
 * `stale` when `freshUntil` has passed.
 */
export type MonitorStatusEffective = StatusState;

/** Why a monitor has no current status. */
export type StatusUnknownReason = 'no_checks' | 'config_changed';

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

export type Monitor = {
  id: string;
  name: string;
  lifecycle: Lifecycle;
  configVersion: number;
  /** Seconds between scheduled checks. */
  intervalSeconds: number;
  check: CheckConfig;
  incidentPolicy: IncidentPolicy;
  openIncident: { id: string; openedAt: string } | null;
  createdAt: string;
  updatedAt: string;
  pausedAt?: string;
  archivedAt?: string;
};

export type Observation = {
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
  request: CheckConfig;
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
  check: CheckInput;
  intervalSeconds?: number;
  incidentPolicy?: IncidentPolicy;
};

export type UpdateMonitorInput = {
  expectedConfigVersion: number;
  name: string;
  check: CheckInput;
  intervalSeconds?: number;
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

function isOutcome(value: unknown): value is CheckOutcome {
  return value === 'healthy' || value === 'failing' || value === 'checker_problem';
}

function isStatusState(value: unknown): value is StatusState {
  return (
    value === 'healthy' ||
    value === 'failing' ||
    value === 'checker_problem' ||
    value === 'stale' ||
    value === 'unknown' ||
    value === 'paused' ||
    value === 'archived'
  );
}

function isStatusUnknownReason(value: unknown): value is StatusUnknownReason {
  return value === 'no_checks' || value === 'config_changed';
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
  if (!('name' in value) || typeof value.name !== 'string') invalid();
  if (!('lifecycle' in value) || !isLifecycle(value.lifecycle)) invalid();
  if (!('configVersion' in value) || !isInteger(value.configVersion) || value.configVersion < 1) {
    invalid();
  }
  if (
    !('intervalSeconds' in value) ||
    !isInteger(value.intervalSeconds) ||
    value.intervalSeconds < 1
  ) {
    invalid();
  }
  if (!('check' in value)) invalid();
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

  const monitor: Monitor = {
    id: value.id,
    name: value.name,
    lifecycle: value.lifecycle,
    configVersion: value.configVersion,
    intervalSeconds: value.intervalSeconds,
    check: parseCheckConfig(value.check),
    incidentPolicy: { openAfter: policy.openAfter, recoverAfter: policy.recoverAfter },
    openIncident:
      openIncident === null
        ? null
        : { id: String(openIncident.id), openedAt: String(openIncident.openedAt) },
    createdAt: value.createdAt,
    updatedAt: value.updatedAt,
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
  if (!('reason' in value) || !isNonEmptyString(value.reason)) invalid();
  if (!('bodyBytesRead' in value) || !isInteger(value.bodyBytesRead) || value.bodyBytesRead < 0) {
    invalid();
  }
  if (!('bodyTruncated' in value) || typeof value.bodyTruncated !== 'boolean') invalid();

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

  const observation: Observation = {
    id: value.id,
    monitorId: value.monitorId,
    configVersion: value.configVersion,
    initiatedBy: value.initiatedBy,
    trigger,
    dueAt,
    counted: value.counted,
    notCountedReason,
    request: parseCheckConfig(value.request),
    startedAt: value.startedAt,
    completedAt: value.completedAt,
    durationMs: value.durationMs,
    outcome: value.outcome,
    reason: value.reason,
    bodyBytesRead: value.bodyBytesRead,
    bodyTruncated: value.bodyTruncated,
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
  return { intervalSeconds, defaultIntervalSeconds: value.defaultIntervalSeconds };
}

/** `GET /api/monitors` — every lifecycle, oldest first (server order). */
export function listMonitors(signal?: AbortSignal): Promise<MonitorRecord[]> {
  return requestJson(MONITORS_PATH, parseMonitorList, { signal });
}

/** `POST /api/monitors` (201 `Monitor`). */
export function createMonitor(
  input: CreateMonitorInput,
  signal?: AbortSignal,
): Promise<MonitorRecord> {
  return requestJson(MONITORS_PATH, parseMonitorRecord, { method: 'POST', body: input, signal });
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

/** `GET /api/intervals` — the intervals this process offers, ascending. */
export function listIntervals(signal?: AbortSignal): Promise<Intervals> {
  return requestJson('/api/intervals', parseIntervals, { signal });
}
