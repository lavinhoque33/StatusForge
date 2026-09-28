import type { MonitorRecord, MonitorStatusEffective } from '../api/monitors';
import { intervalLabel } from './intervals';
import { formatLocalWithOffset, formatRelativeAge } from './time';

/** Backend owns missing evidence; the client advances only late and stale display deadlines. */
export function heartbeatState(monitor: MonitorRecord, now: number): MonitorStatusEffective {
  const status = monitor.status;
  if (
    monitor.kind !== 'heartbeat' ||
    status.state === 'paused' ||
    status.state === 'archived' ||
    status.state === 'failing' ||
    monitor.expectation === null
  )
    return status.state;
  if (now >= Date.parse(monitor.expectation.staleAt)) return 'stale';
  if (
    now >= Date.parse(monitor.expectation.lateAt) &&
    (status.state === 'healthy' || status.state === 'unknown')
  )
    return 'late';
  return status.state;
}

export function heartbeatDeadlines(monitor: MonitorRecord): number[] {
  if (
    monitor.kind !== 'heartbeat' ||
    monitor.expectation === null ||
    monitor.lifecycle !== 'active' ||
    monitor.status.state === 'failing'
  )
    return [];
  return [Date.parse(monitor.expectation.lateAt), Date.parse(monitor.expectation.staleAt)];
}
export function heartbeatDeadline(monitor: MonitorRecord, now: number): number | null {
  if (
    monitor.kind !== 'heartbeat' ||
    monitor.expectation === null ||
    monitor.lifecycle !== 'active' ||
    monitor.status.state === 'failing'
  )
    return null;
  const times = [Date.parse(monitor.expectation.lateAt), Date.parse(monitor.expectation.staleAt)];
  return times.find((time) => time > now) ?? null;
}

export function heartbeatHeadline(monitor: MonitorRecord, now: number): string {
  const status = monitor.status;
  const expectation = monitor.expectation;
  const state = heartbeatState(monitor, now);
  if (state === 'paused') return 'Paused';
  if (state === 'archived') return 'Archived';
  if (state === 'stale') return 'Stale — deadlines not being checked';
  if (state === 'failing' && status.reason === 'reported_failure') {
    const observation = status.observation;
    return `Failing — job reported failure ${observation ? formatRelativeAge(observation.completedAt, now) : 'unknown time'} (exit ${observation?.report?.exitCode ?? 'unknown'})`;
  }
  if (state === 'failing' && status.reason === 'missing') {
    const missed = status.observation;
    const since =
      monitor.heartbeat!.lastReportAt === null
        ? 'no report received yet'
        : `no report since ${formatLocalWithOffset(new Date(monitor.heartbeat!.lastReportAt))}`;
    return `Missing — ${since} (due ${missed?.dueAt ? formatLocalWithOffset(new Date(missed.dueAt)) : 'unknown'}, grace ${intervalLabel(monitor.heartbeat!.graceSeconds)})`;
  }
  if (expectation === null) return 'Waiting for first report';
  const due = formatLocalWithOffset(new Date(expectation.dueAt));
  if (state === 'late')
    return `Late — due at ${due}; missing after ${formatLocalWithOffset(new Date(expectation.missingAt))}`;
  if (state === 'unknown') return `Waiting for first report — due by ${due}`;
  if (monitor.heartbeat?.lastReportAt === null) return `Waiting for first report — due by ${due}`;
  return `On time — last report ${formatRelativeAge(monitor.heartbeat!.lastReportAt, now)}; next due by ${due}`;
}
