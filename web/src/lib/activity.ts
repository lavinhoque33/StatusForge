import type { MonitorRecord, Observation } from '../api/monitors';

export type ActivityReport = { monitor: MonitorRecord; observation: Observation };
/** Bound per-monitor fan-out to 50 recent observations and the rendered result to 100 reports. */
export function mergeActivity(monitors: MonitorRecord[], lists: Observation[][]): ActivityReport[] {
  return monitors
    .flatMap((monitor, index) =>
      lists[index]
        .filter((observation) => observation.kind === 'heartbeat_report')
        .map((observation) => ({ monitor, observation })),
    )
    .sort(
      (a, b) =>
        Date.parse(b.observation.completedAt) - Date.parse(a.observation.completedAt) ||
        a.observation.id.localeCompare(b.observation.id),
    )
    .slice(0, 100);
}
