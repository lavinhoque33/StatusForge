import { ApiInvalidResponseError, requestJson } from './http';

export type SystemInfo = {
  version: string;
  table: string;
  dataFormat: number;
  dataFormatWrittenBy: string;
  upgradedFrom: string | null;
  backfill: {
    state: 'running' | 'done';
    stamped: number;
    startedAt: string | null;
    finishedAt: string | null;
  };
  ttl: {
    status: 'ENABLED' | 'ENABLING' | 'DISABLED' | 'DISABLING' | 'unknown';
    attribute: string | null;
  };
  retention: {
    record: string;
    label: string;
    days: number;
    startsFrom: string;
    protectedWhile: string;
  }[];
  housekeeping: {
    intervalSeconds: number;
    lastRunAt: string | null;
    pendingRetentionJobs: number;
    pendingDeletions: number;
  };
  limits: {
    workers: number;
    minIntervalSeconds: number;
    deliveryWorkers: number;
    deliveryRetrySchedule: string[];
    reminderIntervalSeconds: number;
    livenessIntervalSeconds: number;
    allowedTargets: string;
    notifyUrl: string;
    historyScanBound: number;
    summaryObservationLimit: number;
    summaryGapLimit: number;
    summaryWindows: number[];
  };
  demo: boolean;
};

const invalid = (): never => {
  throw new ApiInvalidResponseError();
};
const object = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : invalid();
const text = (value: unknown): string => (typeof value === 'string' ? value : invalid());
const count = (value: unknown): number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : invalid();
const nullableText = (value: unknown): string | null => (value === null ? null : text(value));
const stringList = (value: unknown): string[] =>
  Array.isArray(value) && value.every((entry) => typeof entry === 'string')
    ? (value as string[])
    : invalid();
const numberList = (value: unknown): number[] =>
  Array.isArray(value) && value.every((entry) => typeof entry === 'number')
    ? (value as number[])
    : invalid();
export function parseSystem(value: unknown): SystemInfo {
  const root = object(value);
  const backfill = object(root.backfill);
  const ttl = object(root.ttl);
  const housekeeping = object(root.housekeeping);
  const limits = object(root.limits);
  if (backfill.state !== 'running' && backfill.state !== 'done') invalid();
  if (
    !['ENABLED', 'ENABLING', 'DISABLED', 'DISABLING', 'unknown'].includes(String(ttl.status)) ||
    typeof root.demo !== 'boolean'
  )
    invalid();
  const retention: unknown[] = Array.isArray(root.retention) ? root.retention : invalid();
  return {
    version: text(root.version),
    table: text(root.table),
    dataFormat: count(root.dataFormat),
    dataFormatWrittenBy: text(root.dataFormatWrittenBy),
    upgradedFrom: nullableText(root.upgradedFrom),
    backfill: {
      state: backfill.state as 'running' | 'done',
      stamped: count(backfill.stamped),
      startedAt: nullableText(backfill.startedAt),
      finishedAt: nullableText(backfill.finishedAt),
    },
    ttl: {
      status: ttl.status as SystemInfo['ttl']['status'],
      attribute: nullableText(ttl.attribute),
    },
    retention: retention.map((entry: unknown) => {
      const row = object(entry);
      return {
        record: text(row.record),
        label: text(row.label),
        days: count(row.days),
        startsFrom: text(row.startsFrom),
        protectedWhile: text(row.protectedWhile),
      };
    }),
    housekeeping: {
      intervalSeconds: count(housekeeping.intervalSeconds),
      lastRunAt: nullableText(housekeeping.lastRunAt),
      pendingRetentionJobs: count(housekeeping.pendingRetentionJobs),
      pendingDeletions: count(housekeeping.pendingDeletions),
    },
    limits: {
      workers: count(limits.workers),
      minIntervalSeconds: count(limits.minIntervalSeconds),
      deliveryWorkers: count(limits.deliveryWorkers),
      deliveryRetrySchedule: stringList(limits.deliveryRetrySchedule),
      reminderIntervalSeconds: count(limits.reminderIntervalSeconds),
      livenessIntervalSeconds: count(limits.livenessIntervalSeconds),
      allowedTargets: text(limits.allowedTargets),
      notifyUrl: text(limits.notifyUrl),
      historyScanBound: count(limits.historyScanBound),
      summaryObservationLimit: count(limits.summaryObservationLimit),
      summaryGapLimit: count(limits.summaryGapLimit),
      summaryWindows: numberList(limits.summaryWindows),
    },
    demo: root.demo as boolean,
  };
}
export const getSystem = (signal?: AbortSignal): Promise<SystemInfo> =>
  requestJson('/api/system', parseSystem, { signal });
