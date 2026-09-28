import type {
  Attempt,
  Evidence,
  Incident,
  IncidentEvent,
  IncidentResolution,
} from '../api/incidents';
import type { Gap } from '../api/monitors';
import { gapReasonWords } from './reasons';
import { formatLocalWithOffset } from './time';
import { outcomeWord } from './outcomes';

export function resolutionWords(resolution: IncidentResolution | null): string {
  switch (resolution) {
    case 'recovered':
      return 'Recovered';
    case 'archived':
      return 'Ended — monitor archived';
    case null:
      return 'Open';
  }
}
export function attemptResultWords(attempt: Attempt): string {
  switch (attempt.result) {
    case 'in_flight':
      return 'In flight';
    case 'delivered':
      return 'Delivered';
    case 'http_error':
      return `Receiver error (HTTP ${attempt.httpStatus ?? 'unknown'})`;
    case 'rejected':
      return `Rejected (HTTP ${attempt.httpStatus ?? 'unknown'})`;
    case 'timeout':
      return 'Timed out';
    case 'connection_refused':
      return 'Receiver unreachable';
    case 'outcome_unknown':
      return 'Outcome unknown — may have been received';
    case 'connection_error':
      return 'Connection error';
    case 'refused_by_policy':
      return 'Destination refused by policy';
    case 'process_stopped':
      return 'Interrupted — StatusForge stopped';
  }
}
export function eventWords(event: IncidentEvent): string {
  switch (event.type) {
    case 'opened':
      return 'Opened';
    case 'paused':
      return 'Monitoring paused';
    case 'resumed':
      return 'Monitoring resumed';
    case 'config_changed':
      return `Check configuration changed (v${event.details.fromVersion ?? '?'} → v${event.details.toVersion ?? '?'})`;
    case 'maintenance_started':
      return 'Maintenance started';
    case 'maintenance_ended':
      return 'Maintenance ended';
    case 'resolved':
      return resolutionWords(event.details.resolution ?? null);
  }
}
export function evidenceWords(evidence: Evidence): string {
  const reason =
    evidence.reason === 'wrong_status'
      ? `wrong status${evidence.observedStatus === null ? '' : `: got ${evidence.observedStatus}`}`
      : evidence.reason === 'refused_by_policy'
        ? 'target refused by policy'
        : evidence.reason.split('_').join(' ');
  return `${outcomeWord(evidence.outcome)} — ${reason}${evidence.observedStatus === null || evidence.reason === 'wrong_status' ? '' : ` (HTTP ${evidence.observedStatus})`}; ${evidence.initiatedBy}, v${evidence.configVersion}`;
}
export type IncidentTimelineItem =
  { kind: 'event'; at: string; event: IncidentEvent } | { kind: 'gap'; at: string; gap: Gap };
export function mergeIncidentTimeline(
  events: IncidentEvent[],
  gaps: Gap[],
): IncidentTimelineItem[] {
  return [
    ...events.map((event): IncidentTimelineItem => ({ kind: 'event', at: event.at, event })),
    ...gaps.map((gap): IncidentTimelineItem => ({ kind: 'gap', at: gap.fromDueAt, gap })),
  ].sort((a, b) => a.at.localeCompare(b.at) || (a.kind === 'event' ? -1 : 1));
}
export function durationWords(incident: Incident, now: number): string {
  const ms = Math.max(
    0,
    (incident.resolvedAt === null ? now : Date.parse(incident.resolvedAt)) -
      Date.parse(incident.openedAt),
  );
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  return hours < 24
    ? `${hours} h ${minutes % 60} min`
    : `${Math.floor(hours / 24)} d ${hours % 24} h`;
}
export function timelineWords(item: IncidentTimelineItem): string {
  if (item.kind === 'event') return eventWords(item.event);
  const gap = item.gap;
  return `Missed ${gap.missedCount} scheduled check${gap.missedCount === 1 ? '' : 's'} — ${gapReasonWords(gap.reason)} (through ${formatLocalWithOffset(new Date(gap.toDueAt))})`;
}
