import { ApiInvalidResponseError, requestJson } from './http';

export type DeletionStatus = {
  state: 'waiting_for_notifications' | 'deleting';
  requestedAt: string;
  updatedAt: string;
  removedItems: number;
};

export function parseDeletion(value: unknown): DeletionStatus | null {
  if (value === null) return null;
  if (typeof value !== 'object' || Array.isArray(value)) throw new ApiInvalidResponseError();
  const status = value as Record<string, unknown>;
  if (
    (status.state !== 'waiting_for_notifications' && status.state !== 'deleting') ||
    typeof status.requestedAt !== 'string' ||
    Number.isNaN(Date.parse(status.requestedAt)) ||
    typeof status.updatedAt !== 'string' ||
    Number.isNaN(Date.parse(status.updatedAt)) ||
    typeof status.removedItems !== 'number' ||
    !Number.isSafeInteger(status.removedItems) ||
    status.removedItems < 0
  )
    throw new ApiInvalidResponseError();
  return status as DeletionStatus;
}

export type DeletionResource = 'monitors' | 'applications';
const path = (resource: DeletionResource, id: string) =>
  `/api/${resource}/${encodeURIComponent(id)}/deletion`;

export const requestDeletion = (resource: DeletionResource, id: string, confirmName: string) =>
  requestJson(path(resource, id), (value) => parseDeletion(value) ?? invalid(), {
    method: 'POST',
    body: { confirmName },
  });
export const getDeletion = (resource: DeletionResource, id: string, signal?: AbortSignal) =>
  requestJson(path(resource, id), (value) => parseDeletion(value) ?? invalid(), { signal });

function invalid(): never {
  throw new ApiInvalidResponseError();
}
