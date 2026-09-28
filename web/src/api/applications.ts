import { ApiInvalidResponseError, requestJson, requestNoContent } from './http';
import { requestApplicationMembership, type MonitorRecord } from './monitors';
import { parseDeletion, type DeletionStatus } from './deletion';

export type Application = {
  id: string;
  name: string;
  token: { hint: string; createdAt: string } | null;
  members: { id: string; name: string; kind: 'http' | 'heartbeat' }[];
  createdAt: string;
  updatedAt: string;
  archivedAt: string | null;
  deletion: DeletionStatus | null;
};
export type Marker = {
  id: string;
  applicationId: string;
  version: string;
  description: string | null;
  link: string | null;
  deployedAt: string | null;
  deploymentId: string | null;
  source: 'ingest' | 'manual';
  reportedAt: string;
};
export type NearbyDeployment = Marker & { offsetSeconds: number };
export type MarkerInput = {
  version: string;
  description?: string;
  link?: string;
  deployedAt?: string;
  deploymentId?: string;
};
const invalid = (): never => {
  throw new ApiInvalidResponseError();
};
const object = (v: unknown): Record<string, unknown> =>
  v !== null && typeof v === 'object' && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : invalid();
const text = (v: unknown): string => (typeof v === 'string' ? v : invalid());
const time = (v: unknown): string =>
  typeof v === 'string' &&
  /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/.test(v) &&
  !Number.isNaN(Date.parse(v))
    ? v
    : invalid();
const optionalText = (v: unknown): string | null => (v === null ? null : text(v));
const optionalTime = (v: unknown): string | null => (v === null ? null : time(v));
const list = <T>(v: unknown, parse: (v: unknown) => T): T[] =>
  Array.isArray(v) ? v.map(parse) : invalid();
export function parseApplication(value: unknown): Application {
  const v = object(value);
  return {
    id: text(v.id),
    name: text(v.name),
    token:
      v.token === null
        ? null
        : (() => {
            const t = object(v.token);
            return { hint: text(t.hint), createdAt: time(t.createdAt) };
          })(),
    members: list(v.members, (entry) => {
      const m = object(entry);
      if (m.kind !== 'http' && m.kind !== 'heartbeat') invalid();
      return { id: text(m.id), name: text(m.name), kind: m.kind as 'http' | 'heartbeat' };
    }),
    createdAt: time(v.createdAt),
    updatedAt: time(v.updatedAt),
    archivedAt: optionalTime(v.archivedAt),
    deletion: parseDeletion(v.deletion),
  };
}
export function parseMarker(value: unknown): Marker {
  const v = object(value);
  if (v.source !== 'ingest' && v.source !== 'manual') invalid();
  const link = optionalText(v.link);
  if (link !== null && !safeMarkerLink(link)) invalid();
  return {
    id: text(v.id),
    applicationId: text(v.applicationId),
    version: text(v.version),
    description: optionalText(v.description),
    link,
    deployedAt: optionalTime(v.deployedAt),
    deploymentId: optionalText(v.deploymentId),
    source: v.source as Marker['source'],
    reportedAt: time(v.reportedAt),
  };
}
export function safeMarkerLink(link: string): boolean {
  try {
    const url = new URL(link);
    return (
      (url.protocol === 'http:' || url.protocol === 'https:') &&
      !!url.hostname &&
      !url.username &&
      !url.password
    );
  } catch {
    return false;
  }
}
export function parseNearbyDeployment(value: unknown): NearbyDeployment {
  const v = object(value);
  if (typeof v.offsetSeconds !== 'number' || !Number.isFinite(v.offsetSeconds)) invalid();
  return { ...parseMarker(v), offsetSeconds: v.offsetSeconds as number };
}
const path = (id: string) => `/api/applications/${encodeURIComponent(id)}`;
export const listApplications = (signal?: AbortSignal): Promise<Application[]> =>
  requestJson('/api/applications', (body) => list(object(body).applications, parseApplication), {
    signal,
  });
export const getApplication = (id: string, signal?: AbortSignal): Promise<Application> =>
  requestJson(path(id), parseApplication, { signal });
export const createApplication = (name: string): Promise<Application & { issuedToken: string }> =>
  requestJson(
    '/api/applications',
    (body) => ({ ...parseApplication(body), issuedToken: text(object(body).issuedToken) }),
    { method: 'POST', body: { name } },
  );
export const renameApplication = (id: string, name: string): Promise<Application> =>
  requestJson(path(id), parseApplication, { method: 'PATCH', body: { name } });
export const archiveApplication = (id: string): Promise<Application> =>
  requestJson(`${path(id)}/archive`, parseApplication, { method: 'POST' });
export const rotateApplicationToken = (
  id: string,
): Promise<{ token: string; hint: string; createdAt: string }> =>
  requestJson(
    `${path(id)}/token`,
    (body) => {
      const v = object(body);
      return { token: text(v.token), hint: text(v.hint), createdAt: time(v.createdAt) };
    },
    { method: 'POST' },
  );
export const revokeApplicationToken = (id: string): Promise<void> =>
  requestNoContent(`${path(id)}/token`);
export const listMarkers = (id: string, signal?: AbortSignal): Promise<Marker[]> =>
  requestJson(
    `${path(id)}/deployments?limit=50`,
    (body) => list(object(body).deployments, parseMarker),
    { signal },
  );
export const createMarker = (id: string, body: MarkerInput): Promise<Marker> =>
  requestJson(`${path(id)}/deployments`, parseMarker, { method: 'POST', body });
export const setMembership = (
  monitorId: string,
  applicationId: string | null,
): Promise<MonitorRecord> => requestApplicationMembership(monitorId, applicationId);
