import { ApiInvalidResponseError, requestJson } from './http';
import {
  parseIncident,
  parseNotification,
  type Incident,
  type AttentionNotification,
} from './incidents';
import { parseMonitorStatus, type MonitorKind, type MonitorStatus } from './monitors';

export type Span = { from: string; to: string | null };
export type SummaryWindow = '24h' | '7d';
export type SummaryCounts = {
  expected: number;
  recorded: number;
  notObserved: number;
  maintenance: number;
  notCounted: number;
  notCountedReasons: Record<string, number>;
  outcomes: { healthy: number; failing: number; checkerProblem: number };
  manualChecks: number;
  pausedSeconds: number;
};
export type Latency = {
  samples: number;
  medianMs: number | null;
  p95Ms: number | null;
  maxMs: number | null;
  noResponse: number;
  checkerProblems: number;
};
export type SummaryBucket = {
  from: string;
  to: string;
  expected: number;
  recorded: number;
  notObserved: number;
  maintenance: number;
  notCounted: number;
  healthy: number;
  failing: number;
  checkerProblem: number;
  pausedSeconds: number;
  latency: Pick<Latency, 'samples' | 'medianMs' | 'p95Ms' | 'maxMs'>;
};
export type HttpSummary = {
  monitorId: string;
  kind: 'http';
  window: SummaryWindow;
  from: string;
  to: string;
  evaluatedAt: string;
  truncated: boolean;
  coveredFrom: string;
  bucketSeconds: number;
  lifecycleHistoryFrom: string | null;
  pendingSince: string | null;
  coverage: SummaryCounts;
  latency: Latency;
  outages: Span[];
  maintenanceWindows: (Span & { id: string })[];
  buckets: SummaryBucket[];
};
export type MonitorRef = {
  id: string;
  name: string;
  kind: MonitorKind;
  applicationId: string | null;
  applicationName: string | null;
};
export type Overview = {
  evaluatedAt: string;
  receiveOutages: Span[];
  openIncidents: { monitor: MonitorRef; incident: Incident }[];
  failingWithoutIncident: { monitor: MonitorRef; status: MonitorStatus }[];
  coverageProblems: { monitor: MonitorRef; status: MonitorStatus }[];
  notifications: { count: number; items: AttentionNotification[] };
  recentRecoveries: { monitor: MonitorRef; incident: Incident }[];
  counts: {
    active: number;
    paused: number;
    archived: number;
    byState: Record<
      'healthy' | 'late' | 'failing' | 'checker_problem' | 'stale' | 'unknown',
      number
    >;
  };
  limits: {
    openIncidents: number;
    recentRecoveries: number;
    notifications: number;
    recoveryWindowHours: number;
  };
};

function invalid(): never {
  throw new ApiInvalidResponseError();
}
function obj(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalid();
  return value as Record<string, unknown>;
}
function text(value: unknown): string {
  if (typeof value !== 'string' || !value) invalid();
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
function nullableTime(value: unknown): string | null {
  return value === null ? null : time(value);
}
function nullableText(value: unknown): string | null {
  return value === null ? null : text(value);
}
function count(value: unknown): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) invalid();
  return value;
}
function nullableCount(value: unknown): number | null {
  return value === null ? null : count(value);
}
function flag(value: unknown): boolean {
  if (typeof value !== 'boolean') invalid();
  return value;
}
function list<T>(value: unknown, parse: (v: unknown) => T): T[] {
  if (!Array.isArray(value)) invalid();
  return value.map(parse);
}
function span(value: unknown): Span {
  const v = obj(value);
  return { from: time(v.from), to: nullableTime(v.to) };
}
function latency(value: unknown): Latency {
  const v = obj(value);
  return {
    samples: count(v.samples),
    medianMs: nullableCount(v.medianMs),
    p95Ms: nullableCount(v.p95Ms),
    maxMs: nullableCount(v.maxMs),
    noResponse: count(v.noResponse),
    checkerProblems: count(v.checkerProblems),
  };
}
function bucket(value: unknown): SummaryBucket {
  const v = obj(value),
    l = obj(v.latency);
  return {
    from: time(v.from),
    to: time(v.to),
    expected: count(v.expected),
    recorded: count(v.recorded),
    notObserved: count(v.notObserved),
    maintenance: count(v.maintenance),
    notCounted: count(v.notCounted),
    healthy: count(v.healthy),
    failing: count(v.failing),
    checkerProblem: count(v.checkerProblem),
    pausedSeconds: count(v.pausedSeconds),
    latency: {
      samples: count(l.samples),
      medianMs: nullableCount(l.medianMs),
      p95Ms: nullableCount(l.p95Ms),
      maxMs: nullableCount(l.maxMs),
    },
  };
}
export function parseHttpSummary(value: unknown): HttpSummary {
  const v = obj(value),
    c = obj(v.coverage),
    o = obj(c.outcomes),
    reasons = obj(c.notCountedReasons);
  if (v.kind !== 'http' || (v.window !== '24h' && v.window !== '7d')) invalid();
  const notCountedReasons = Object.fromEntries(
    Object.entries(reasons).map(([key, value]) => [key, count(value)]),
  );
  return {
    monitorId: text(v.monitorId),
    kind: 'http',
    window: v.window,
    from: time(v.from),
    to: time(v.to),
    evaluatedAt: time(v.evaluatedAt),
    truncated: flag(v.truncated),
    coveredFrom: time(v.coveredFrom),
    bucketSeconds: count(v.bucketSeconds),
    lifecycleHistoryFrom: nullableTime(v.lifecycleHistoryFrom),
    pendingSince: nullableTime(v.pendingSince),
    coverage: {
      expected: count(c.expected),
      recorded: count(c.recorded),
      notObserved: count(c.notObserved),
      maintenance: count(c.maintenance),
      notCounted: count(c.notCounted),
      notCountedReasons,
      outcomes: {
        healthy: count(o.healthy),
        failing: count(o.failing),
        checkerProblem: count(o.checkerProblem),
      },
      manualChecks: count(c.manualChecks),
      pausedSeconds: count(c.pausedSeconds),
    },
    latency: latency(v.latency),
    outages: list(v.outages, span),
    maintenanceWindows: list(v.maintenanceWindows, (item) => ({
      ...span(item),
      id: text(obj(item).id),
    })),
    buckets: list(v.buckets, bucket),
  };
}
function monitorRef(value: unknown): MonitorRef {
  const v = obj(value);
  if (v.kind !== 'http' && v.kind !== 'heartbeat') invalid();
  return {
    id: text(v.id),
    name: text(v.name),
    kind: v.kind,
    applicationId: nullableText(v.applicationId),
    applicationName: nullableText(v.applicationName),
  };
}
function incidentRow(value: unknown) {
  const v = obj(value);
  return { monitor: monitorRef(v.monitor), incident: parseIncident(v.incident) };
}
function statusRow(value: unknown) {
  const v = obj(value);
  return { monitor: monitorRef(v.monitor), status: parseMonitorStatus(v.status) };
}
function attention(value: unknown): AttentionNotification {
  const v = obj(value);
  return {
    ...parseNotification(v),
    monitorId: text(v.monitorId),
    monitorName: text(v.monitorName),
    incidentId: text(v.incidentId),
  };
}
export function parseOverview(value: unknown): Overview {
  const v = obj(value),
    n = obj(v.notifications),
    c = obj(v.counts),
    states = obj(c.byState),
    limits = obj(v.limits);
  return {
    evaluatedAt: time(v.evaluatedAt),
    receiveOutages: list(v.receiveOutages, span),
    openIncidents: list(v.openIncidents, incidentRow),
    failingWithoutIncident: list(v.failingWithoutIncident, statusRow),
    coverageProblems: list(v.coverageProblems, statusRow),
    notifications: { count: count(n.count), items: list(n.items, attention) },
    recentRecoveries: list(v.recentRecoveries, incidentRow),
    counts: {
      active: count(c.active),
      paused: count(c.paused),
      archived: count(c.archived),
      byState: {
        healthy: count(states.healthy),
        late: count(states.late),
        failing: count(states.failing),
        checker_problem: count(states.checker_problem),
        stale: count(states.stale),
        unknown: count(states.unknown),
      },
    },
    limits: {
      openIncidents: count(limits.openIncidents),
      recentRecoveries: count(limits.recentRecoveries),
      notifications: count(limits.notifications),
      recoveryWindowHours: count(limits.recoveryWindowHours),
    },
  };
}
export function getOverview(signal?: AbortSignal): Promise<Overview> {
  return requestJson('/api/overview', parseOverview, { signal });
}
export function getHttpSummary(
  id: string,
  window: SummaryWindow,
  signal?: AbortSignal,
): Promise<HttpSummary> {
  return requestJson(
    `/api/monitors/${encodeURIComponent(id)}/summary?window=${window}`,
    parseHttpSummary,
    { signal },
  );
}
