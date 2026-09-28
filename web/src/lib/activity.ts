import type { MonitorRecord, Observation } from '../api/monitors';
import type { Application, Marker } from '../api/applications';

export type ActivityReport = { monitor: MonitorRecord; observation: Observation };
export type ActivityDeployment = { application: Application; marker: Marker };
export type ActivityItem =
  ({ kind: 'report' } & ActivityReport) | ({ kind: 'deployment' } & ActivityDeployment);
/** Merge bounded per-source reads; filtering is applied to the full merged window. */
export function mergeActivityItems(
  reports: ActivityReport[],
  applications: Application[],
  lists: Marker[][],
): ActivityItem[] {
  return [
    ...reports.map((report): ActivityItem => ({ kind: 'report', ...report })),
    ...applications.flatMap((application, index) =>
      lists[index].map((marker): ActivityItem => ({ kind: 'deployment', application, marker })),
    ),
  ]
    .sort((a, b) => {
      const aAt = a.kind === 'report' ? a.observation.completedAt : a.marker.reportedAt;
      const bAt = b.kind === 'report' ? b.observation.completedAt : b.marker.reportedAt;
      const aId = a.kind === 'report' ? a.observation.id : a.marker.id;
      const bId = b.kind === 'report' ? b.observation.id : b.marker.id;
      return Date.parse(bAt) - Date.parse(aAt) || aId.localeCompare(bId);
    })
    .slice(0, 100);
}
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
