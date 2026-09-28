import type { Gap, Observation } from '../api/monitors';

/** One merged timeline entry: an observation or a gap, with its sort instant. */
export type TimelineEntry =
  | { kind: 'observation'; key: string; instant: number; observation: Observation }
  | { kind: 'gap'; key: string; instant: number; gap: Gap };

/**
 * Merge observations and gaps into one newest-first timeline.
 *
 * Observations sort by `startedAt` (when the check ran); gaps sort by
 * `toDueAt` (the newest missed slot the range covers), so a gap sits among the
 * observations around the time it covers. Adjacent gaps stay separate rows.
 */
export function mergeTimeline(observations: Observation[], gaps: Gap[]): TimelineEntry[] {
  const entries: TimelineEntry[] = [
    ...observations.map((observation): TimelineEntry => ({
      kind: 'observation',
      key: observation.id,
      instant: Date.parse(observation.startedAt),
      observation,
    })),
    ...gaps.map((gap): TimelineEntry => ({
      kind: 'gap',
      key: gap.id,
      instant: Date.parse(gap.toDueAt),
      gap,
    })),
  ];
  return entries.sort((a, b) => b.instant - a.instant);
}
