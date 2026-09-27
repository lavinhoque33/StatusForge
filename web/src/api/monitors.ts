/**
 * Typed client for the M1 monitor routes.
 *
 * Responses are validated property by property before they reach the interface:
 * an unknown lifecycle or outcome is a protocol violation and is reported as an
 * unexpected response rather than guessed at. Reason codes stay strings so a
 * vocabulary addition cannot break rendering; the presentation layer maps known
 * codes to words.
 */
import { ApiInvalidResponseError, requestJson } from './http';

/** Observation limit requested by the detail page (contract default is 50). */
export const OBSERVATION_LIMIT = 50;

const MONITORS_PATH = '/api/monitors';

export type Lifecycle = 'active' | 'paused' | 'archived';

export type CheckOutcome = 'healthy' | 'failing' | 'checker_problem';

/** The request settings stored on a monitor and copied onto an observation. */
export type CheckConfig = {
  url: string;
  method: string;
  expectedStatus: number;
  deadlineMs: number;
  maxBodyBytes: number;
};

export type Monitor = {
  id: string;
  name: string;
  lifecycle: Lifecycle;
  configVersion: number;
  check: CheckConfig;
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

/** A list row: the monitor plus its newest non-expired observation. */
export type MonitorListItem = Monitor & { lastObservation: Observation | null };

export type CheckInput = {
  url: string;
  expectedStatus: number;
  deadlineMs: number;
};

export type CreateMonitorInput = {
  name: string;
  check: CheckInput;
};

export type UpdateMonitorInput = {
  expectedConfigVersion: number;
  name: string;
  check: CheckInput;
};

export type LifecycleAction = 'pause' | 'resume' | 'archive';

function invalid(): never {
  throw new ApiInvalidResponseError();
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
  if (!('check' in value)) invalid();
  if (!('createdAt' in value) || !isTimestamp(value.createdAt)) invalid();
  if (!('updatedAt' in value) || !isTimestamp(value.updatedAt)) invalid();

  const monitor: Monitor = {
    id: value.id,
    name: value.name,
    lifecycle: value.lifecycle,
    configVersion: value.configVersion,
    check: parseCheckConfig(value.check),
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

/** Parse an `Observation` payload; `unknown` is never accepted as an outcome. */
export function parseObservation(value: unknown): Observation {
  if (typeof value !== 'object' || value === null) invalid();
  if (!('id' in value) || !isNonEmptyString(value.id)) invalid();
  if (!('monitorId' in value) || !isNonEmptyString(value.monitorId)) invalid();
  if (!('configVersion' in value) || !isInteger(value.configVersion) || value.configVersion < 1) {
    invalid();
  }
  if (!('initiatedBy' in value) || !isNonEmptyString(value.initiatedBy)) invalid();
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

  const observation: Observation = {
    id: value.id,
    monitorId: value.monitorId,
    configVersion: value.configVersion,
    initiatedBy: value.initiatedBy,
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

function parseMonitorListItem(value: unknown): MonitorListItem {
  const monitor = parseMonitor(value);
  if (typeof value !== 'object' || value === null) invalid();
  if (
    !('lastObservation' in value) ||
    value.lastObservation === null ||
    value.lastObservation === undefined
  ) {
    return { ...monitor, lastObservation: null };
  }
  return { ...monitor, lastObservation: parseObservation(value.lastObservation) };
}

function parseMonitorList(body: unknown): MonitorListItem[] {
  if (typeof body !== 'object' || body === null) invalid();
  if (!('monitors' in body) || !Array.isArray(body.monitors)) invalid();
  return body.monitors.map(parseMonitorListItem);
}

function parseObservationList(body: unknown): Observation[] {
  if (typeof body !== 'object' || body === null) invalid();
  if (!('observations' in body) || !Array.isArray(body.observations)) invalid();
  return body.observations.map(parseObservation);
}

/** `GET /api/monitors` — every lifecycle, oldest first (server order). */
export function listMonitors(signal?: AbortSignal): Promise<MonitorListItem[]> {
  return requestJson(MONITORS_PATH, parseMonitorList, { signal });
}

/** `POST /api/monitors` (201 `Monitor`). */
export function createMonitor(input: CreateMonitorInput, signal?: AbortSignal): Promise<Monitor> {
  return requestJson(MONITORS_PATH, parseMonitor, { method: 'POST', body: input, signal });
}

/** `GET /api/monitors/{id}`. */
export function getMonitor(id: string, signal?: AbortSignal): Promise<Monitor> {
  return requestJson(`${MONITORS_PATH}/${encodeURIComponent(id)}`, parseMonitor, { signal });
}

/** `PATCH /api/monitors/{id}` guarded by `expectedConfigVersion`. */
export function updateMonitor(
  id: string,
  input: UpdateMonitorInput,
  signal?: AbortSignal,
): Promise<Monitor> {
  return requestJson(`${MONITORS_PATH}/${encodeURIComponent(id)}`, parseMonitor, {
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
): Promise<Monitor> {
  return requestJson(`${MONITORS_PATH}/${encodeURIComponent(id)}/lifecycle`, parseMonitor, {
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
