import type { Observation } from '../api/monitors';
import { observationReason } from '../lib/outcomes';
import { formatLocalWithOffset } from '../lib/time';
import { OutcomeLabel } from './OutcomeLabel';

/** Observation history, newest first. Collapses to stacked rows below 600 px. */
export function ObservationsTable({ observations }: { observations: Observation[] }) {
  if (observations.length === 0) {
    return <p>No checks have been recorded for this monitor.</p>;
  }

  return (
    <div className="table-wrapper">
      <table className="observations">
        <caption>Observations, newest first</caption>
        <thead>
          <tr>
            <th scope="col">Started (local)</th>
            <th scope="col">Outcome</th>
            <th scope="col">Reason</th>
            <th scope="col">Status</th>
            <th scope="col">Duration</th>
            <th scope="col">Version</th>
            <th scope="col">Initiated by</th>
          </tr>
        </thead>
        <tbody>
          {observations.map((observation) => {
            const startedAt = formatLocalWithOffset(new Date(observation.startedAt));
            return (
              <tr key={observation.id}>
                <td data-label="Started">
                  <time dateTime={observation.startedAt} title={startedAt}>
                    {startedAt}
                  </time>
                </td>
                <td data-label="Outcome">
                  <OutcomeLabel observation={observation} />
                </td>
                <td data-label="Reason">{observationReason(observation)}</td>
                <td data-label="Status">{observation.observedStatus ?? '—'}</td>
                <td data-label="Duration">{observation.durationMs} ms</td>
                <td data-label="Version">v{observation.configVersion}</td>
                <td data-label="Initiated by">{observation.initiatedBy}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export default ObservationsTable;
