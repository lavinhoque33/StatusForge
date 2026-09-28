import type { Marker } from '../api/applications';
import type { MonitorRecord } from '../api/monitors';

export function applicationSummary(
  applicationId: string,
  monitors: MonitorRecord[],
  markers: Marker[],
) {
  const members = monitors.filter((monitor) => monitor.applicationId === applicationId);
  const openIncidents = members.filter((monitor) => monitor.openIncident !== null).length;
  const lastDeployment = markers.reduce<Marker | null>(
    (latest, marker) =>
      latest === null ||
      Date.parse(marker.deployedAt ?? marker.reportedAt) >
        Date.parse(latest.deployedAt ?? latest.reportedAt)
        ? marker
        : latest,
    null,
  );
  return { members, openIncidents, lastDeployment };
}
