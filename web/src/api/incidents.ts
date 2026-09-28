import { ApiInvalidResponseError, requestJson } from './http';
import type { CheckOutcome, Gap } from './monitors';
import { parseNearbyDeployment, type NearbyDeployment } from './applications';
/** Maximum attention rows available from one API read; a full page may hide more. */
export const ATTENTION_LIMIT = 200;

export type IncidentState = 'open' | 'resolved';
export type IncidentResolution = 'recovered' | 'archived';
export type IncidentEventType =
  | 'opened'
  | 'paused'
  | 'resumed'
  | 'config_changed'
  | 'maintenance_started'
  | 'maintenance_ended'
  | 'resolved';
export type NotificationKind = 'opened' | 'reminder' | 'resolved';
export type NotificationState =
  'pending' | 'retry_wait' | 'sending' | 'delivered' | 'failed' | 'cancelled';
export type AttemptResult =
  | 'in_flight'
  | 'delivered'
  | 'http_error'
  | 'rejected'
  | 'timeout'
  | 'connection_refused'
  | 'connection_error'
  | 'outcome_unknown'
  | 'refused_by_policy'
  | 'process_stopped';

export type Evidence = {
  kind: 'http_check' | 'heartbeat_report' | 'heartbeat_missed';
  observationId: string;
  startedAt: string;
  initiatedBy: string;
  outcome: CheckOutcome;
  reason: string;
  observedStatus: number | null;
  configVersion: number;
};
export type Incident = {
  id: string;
  monitorId: string;
  applicationId: string | null;
  monitorName: string;
  state: IncidentState;
  resolution: IncidentResolution | null;
  openedAt: string;
  resolvedAt: string | null;
  openingEvidence: Evidence[];
  recoveryEvidence: Evidence[];
  failureCount: number;
  firstFailureAt: string;
  lastFailure: Evidence;
  checkerProblemCount: number;
  lastCheckerProblem: Evidence | null;
  maintenanceObservationCount: number;
  monitoringPaused: boolean;
  inMaintenance: boolean;
  notificationSummary: { delivered: number; pending: number; failed: number };
};
export type IncidentEvent = {
  type: IncidentEventType;
  at: string;
  details: { fromVersion?: number; toVersion?: number; resolution?: IncidentResolution };
};
export type Attempt = {
  number: number;
  startedAt: string;
  completedAt: string | null;
  manual: boolean;
  result: AttemptResult;
  httpStatus: number | null;
  durationMs: number | null;
};
export type Notification = {
  id: string;
  kind: NotificationKind;
  reminderSeq: number | null;
  state: NotificationState;
  createdAt: string;
  nextAttemptAt: string | null;
  deliveredAt: string | null;
  failedAt: string | null;
  cancelledReason: 'incident_resolved' | null;
  attempts: Attempt[];
};
export type AttentionNotification = Notification & {
  monitorId: string;
  monitorName: string;
  incidentId: string;
};
export type IncidentDetail = {
  incident: Incident;
  events: IncidentEvent[];
  gaps: Gap[];
  notifications: Notification[];
  nearbyDeployments: NearbyDeployment[];
};

function invalid(): never {
  throw new ApiInvalidResponseError();
}
function record(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) invalid();
  return value as Record<string, unknown>;
}
function string(value: unknown): string {
  if (typeof value !== 'string') invalid();
  return value;
}
function time(value: unknown): string {
  if (
    typeof value !== 'string' ||
    !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/.test(value) ||
    Number.isNaN(Date.parse(value))
  )
    invalid();
  return value;
}
function optionalTime(value: unknown): string | null {
  return value === null ? null : time(value);
}
function number(value: unknown): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) invalid();
  return value;
}
function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') invalid();
  return value;
}
function choice<T extends string>(value: unknown, choices: readonly T[]): T {
  if (typeof value !== 'string' || !choices.includes(value as T)) invalid();
  return value as T;
}
function array<T>(value: unknown, parse: (item: unknown) => T): T[] {
  if (!Array.isArray(value)) invalid();
  return value.map(parse);
}
function evidence(value: unknown): Evidence {
  const v = record(value);
  return {
    kind:
      v.kind === undefined
        ? 'http_check'
        : choice(v.kind, ['http_check', 'heartbeat_report', 'heartbeat_missed']),
    observationId: string(v.observationId),
    startedAt: time(v.startedAt),
    initiatedBy: string(v.initiatedBy),
    outcome: choice(v.outcome, ['healthy', 'failing', 'checker_problem']),
    reason: string(v.reason),
    observedStatus: v.observedStatus == null ? null : number(v.observedStatus),
    configVersion: number(v.configVersion),
  };
}
export function parseIncident(value: unknown): Incident {
  const v = record(value),
    summary = record(v.notificationSummary);
  return {
    id: string(v.id),
    monitorId: string(v.monitorId),
    applicationId: v.applicationId === null ? null : string(v.applicationId),
    monitorName: string(v.monitorName),
    state: choice(v.state, ['open', 'resolved']),
    resolution:
      v.resolution === null
        ? null
        : choice<IncidentResolution>(v.resolution, ['recovered', 'archived']),
    openedAt: time(v.openedAt),
    resolvedAt: optionalTime(v.resolvedAt),
    openingEvidence: array(v.openingEvidence, evidence),
    recoveryEvidence: array(v.recoveryEvidence, evidence),
    failureCount: number(v.failureCount),
    firstFailureAt: time(v.firstFailureAt),
    lastFailure: evidence(v.lastFailure),
    checkerProblemCount: number(v.checkerProblemCount),
    lastCheckerProblem: v.lastCheckerProblem === null ? null : evidence(v.lastCheckerProblem),
    maintenanceObservationCount: number(v.maintenanceObservationCount),
    monitoringPaused: boolean(v.monitoringPaused),
    inMaintenance: boolean(v.inMaintenance),
    notificationSummary: {
      delivered: number(summary.delivered),
      pending: number(summary.pending),
      failed: number(summary.failed),
    },
  };
}
function event(value: unknown): IncidentEvent {
  const v = record(value),
    details = record(v.details);
  return {
    type: choice(v.type, [
      'opened',
      'paused',
      'resumed',
      'config_changed',
      'maintenance_started',
      'maintenance_ended',
      'resolved',
    ]),
    at: time(v.at),
    details: {
      ...(details.fromVersion === undefined ? {} : { fromVersion: number(details.fromVersion) }),
      ...(details.toVersion === undefined ? {} : { toVersion: number(details.toVersion) }),
      ...(details.resolution === undefined
        ? {}
        : {
            resolution: choice<IncidentResolution>(details.resolution, ['recovered', 'archived']),
          }),
    },
  };
}
function gap(value: unknown): Gap {
  const v = record(value);
  return {
    id: string(v.id),
    monitorId: string(v.monitorId),
    fromDueAt: time(v.fromDueAt),
    toDueAt: time(v.toDueAt),
    missedCount: number(v.missedCount),
    reason: string(v.reason),
    recordedAt: time(v.recordedAt),
  };
}
function attempt(value: unknown): Attempt {
  const v = record(value);
  return {
    number: number(v.number),
    startedAt: time(v.startedAt),
    completedAt: optionalTime(v.completedAt),
    manual: boolean(v.manual),
    result: choice(v.result, [
      'in_flight',
      'delivered',
      'http_error',
      'rejected',
      'timeout',
      'connection_refused',
      'connection_error',
      'outcome_unknown',
      'refused_by_policy',
      'process_stopped',
    ]),
    httpStatus: v.httpStatus == null ? null : number(v.httpStatus),
    durationMs: v.durationMs == null ? null : number(v.durationMs),
  };
}
export function parseNotification(value: unknown): Notification {
  const v = record(value);
  return {
    id: string(v.id),
    kind: choice(v.kind, ['opened', 'reminder', 'resolved']),
    reminderSeq: v.reminderSeq == null ? null : number(v.reminderSeq),
    state: choice(v.state, [
      'pending',
      'retry_wait',
      'sending',
      'delivered',
      'failed',
      'cancelled',
    ]),
    createdAt: time(v.createdAt),
    nextAttemptAt: optionalTime(v.nextAttemptAt),
    deliveredAt: optionalTime(v.deliveredAt),
    failedAt: optionalTime(v.failedAt),
    cancelledReason:
      v.cancelledReason === null
        ? null
        : choice<'incident_resolved'>(v.cancelledReason, ['incident_resolved']),
    attempts: array(v.attempts, attempt),
  };
}
function list(body: unknown): Incident[] {
  return array(record(body).incidents, parseIncident);
}
function attention(body: unknown): AttentionNotification[] {
  return array(record(body).notifications, (value) => {
    const v = record(value);
    return {
      ...parseNotification(v),
      monitorId: string(v.monitorId),
      monitorName: string(v.monitorName),
      incidentId: string(v.incidentId),
    };
  });
}
const monitorPath = (id: string) => `/api/monitors/${encodeURIComponent(id)}`;
const incidentPath = (monitorId: string, incidentId: string) =>
  `${monitorPath(monitorId)}/incidents/${encodeURIComponent(incidentId)}`;
export const incidentLink = (monitorId: string, incidentId: string) =>
  `/incidents/${encodeURIComponent(monitorId)}/${encodeURIComponent(incidentId)}`;
export function listIncidents(
  state: IncidentState | 'all' = 'all',
  limit = 50,
  signal?: AbortSignal,
): Promise<Incident[]> {
  return requestJson(`/api/incidents?state=${state}&limit=${limit}`, list, { signal });
}
export function listMonitorIncidents(
  monitorId: string,
  limit = 5,
  signal?: AbortSignal,
): Promise<Incident[]> {
  return requestJson(`${monitorPath(monitorId)}/incidents?limit=${limit}`, list, { signal });
}
export function getIncident(
  monitorId: string,
  incidentId: string,
  signal?: AbortSignal,
): Promise<IncidentDetail> {
  return requestJson(
    incidentPath(monitorId, incidentId),
    (body) => {
      const v = record(body);
      return {
        incident: parseIncident(v.incident),
        events: array(v.events, event),
        gaps: array(v.gaps, gap),
        notifications: array(v.notifications, parseNotification),
        nearbyDeployments: array(v.nearbyDeployments, parseNearbyDeployment),
      };
    },
    { signal },
  );
}
export function listAttention(limit = 50, signal?: AbortSignal): Promise<AttentionNotification[]> {
  return requestJson(`/api/notifications/attention?limit=${limit}`, attention, { signal });
}
export function retryNotification(
  monitorId: string,
  incidentId: string,
  id: string,
): Promise<Notification> {
  const noteKey = id.slice(incidentId.length + 1).replace('#', '-');
  return requestJson(
    `${incidentPath(monitorId, incidentId)}/notifications/${encodeURIComponent(noteKey)}/retry`,
    parseNotification,
    { method: 'POST' },
  );
}
