import { ApiInvalidResponseError, requestJson } from './http';

export type WindowState = 'scheduled' | 'active' | 'ended' | 'cancelled';
export type Window = {
  id: string;
  monitorId: string;
  startAt: string;
  endAt: string;
  note: string;
  createdAt: string;
  cancelledAt: string | null;
  state: WindowState;
};

function invalid(): never {
  throw new ApiInvalidResponseError();
}

export function parseWindow(value: unknown): Window {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) invalid();
  const v = value as Record<string, unknown>;
  for (const key of ['id', 'monitorId', 'startAt', 'endAt', 'createdAt']) {
    if (typeof v[key] !== 'string' || v[key] === '') invalid();
  }
  for (const key of ['startAt', 'endAt', 'createdAt']) {
    if (Number.isNaN(Date.parse(v[key] as string))) invalid();
  }
  if (typeof v.note !== 'string') invalid();
  if (
    v.cancelledAt !== null &&
    (typeof v.cancelledAt !== 'string' || Number.isNaN(Date.parse(v.cancelledAt)))
  )
    invalid();
  if (
    v.state !== 'scheduled' &&
    v.state !== 'active' &&
    v.state !== 'ended' &&
    v.state !== 'cancelled'
  )
    invalid();
  return v as Window;
}

function parseWindows(body: unknown): Window[] {
  if (
    typeof body !== 'object' ||
    body === null ||
    !('windows' in body) ||
    !Array.isArray(body.windows)
  )
    invalid();
  return body.windows.map(parseWindow);
}

const path = (id: string) => `/api/monitors/${encodeURIComponent(id)}/maintenance`;

export function listMaintenance(id: string, signal?: AbortSignal): Promise<Window[]> {
  return requestJson(path(id), parseWindows, { signal });
}

export function scheduleMaintenance(
  id: string,
  input: { startAt: string; endAt: string; note: string },
): Promise<Window> {
  return requestJson(path(id), parseWindow, { method: 'POST', body: input });
}

export function cancelMaintenance(id: string, windowId: string): Promise<Window> {
  return requestJson(`${path(id)}/${encodeURIComponent(windowId)}/cancel`, parseWindow, {
    method: 'POST',
  });
}
