import type { Gap, Observation } from '../api/monitors';
import type { Window } from '../api/maintenance';
import { observationReason } from '../lib/outcomes';
import { gapReasonWords, notCountedReasonWords } from '../lib/reasons';
import { formatLocalWithOffset } from '../lib/time';
import { mergeTimeline, type TimelineEntry } from '../lib/timeline';
import { OutcomeLabel } from './OutcomeLabel';

function GapRow({ gap }: { gap: Gap }) {
  const from = formatLocalWithOffset(new Date(gap.fromDueAt));
  const to = formatLocalWithOffset(new Date(gap.toDueAt));
  return (
    <tr className="timeline-gap">
      <td data-label="Started">
        <span title={`${from} – ${to}`}>
          {from} – {to}
        </span>
      </td>
      <td data-label="Outcome">
        <span className="timeline-gap-word">
          <span aria-hidden="true">—</span> Gap
        </span>
      </td>
      <td data-label="Reason">
        {gap.missedCount === 1 ? 'Missed 1 check' : `Missed ${gap.missedCount} checks`} —{' '}
        {gapReasonWords(gap.reason)}
      </td>
      <td data-label="Status">—</td>
      <td data-label="Duration">—</td>
      <td data-label="Version">—</td>
      <td data-label="Initiated by">—</td>
    </tr>
  );
}

function BoundaryRow({ at, label }: { at: string; label: string }) {
  return (
    <tr className="timeline-gap">
      <td data-label="Started">
        <time dateTime={at}>{formatLocalWithOffset(new Date(at))}</time>
      </td>
      <td data-label="Outcome">Maintenance</td>
      <td data-label="Reason">{label}</td>
      <td data-label="Status">—</td>
      <td data-label="Duration">—</td>
      <td data-label="Version">—</td>
      <td data-label="Initiated by">—</td>
    </tr>
  );
}

/**
 * Observation and gap history as one timeline, newest first.
 * Collapses to stacked rows below 600 px like the M1 table.
 */
export function TimelineTable({
  observations,
  gaps,
  windows = [],
}: {
  observations: Observation[];
  gaps: Gap[];
  windows?: Window[];
}) {
  if (observations.length === 0 && gaps.length === 0 && windows.length === 0) {
    return <p>No checks have been recorded for this monitor.</p>;
  }

  const timeline: Array<
    TimelineEntry | { kind: 'boundary'; key: string; instant: number; at: string; label: string }
  > = [...mergeTimeline(observations, gaps)];
  for (const window of windows) {
    if (
      window.state === 'scheduled' ||
      (window.cancelledAt !== null && Date.parse(window.cancelledAt) <= Date.parse(window.startAt))
    )
      continue;
    timeline.push({
      kind: 'boundary',
      key: `${window.id}-start`,
      instant: Date.parse(window.startAt),
      at: window.startAt,
      label: 'Maintenance started',
    });
    const endAt = window.cancelledAt ?? window.endAt;
    if (window.state !== 'active')
      timeline.push({
        kind: 'boundary',
        key: `${window.id}-end`,
        instant: Date.parse(endAt),
        at: endAt,
        label: 'Maintenance ended',
      });
  }
  timeline.sort((a, b) => b.instant - a.instant);
  return (
    <div className="table-wrapper">
      <table className="observations">
        <caption>Checks and gaps, newest first</caption>
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
          {timeline.map((entry) =>
            entry.kind === 'boundary' ? (
              <BoundaryRow key={entry.key} at={entry.at} label={entry.label} />
            ) : entry.kind === 'gap' ? (
              <GapRow key={entry.key} gap={entry.gap} />
            ) : (
              <tr
                key={entry.key}
                className={entry.observation.counted ? undefined : 'timeline-not-counted'}
              >
                <td data-label="Started">
                  <time
                    dateTime={entry.observation.startedAt}
                    title={formatLocalWithOffset(new Date(entry.observation.startedAt))}
                  >
                    {formatLocalWithOffset(new Date(entry.observation.startedAt))}
                  </time>
                </td>
                <td data-label="Outcome">
                  <OutcomeLabel observation={entry.observation} />
                  {entry.observation.maintenanceWindowId === null ? null : (
                    <span> · Maintenance</span>
                  )}
                </td>
                <td data-label="Reason">{observationReason(entry.observation)}</td>
                <td data-label="Status">{entry.observation.observedStatus ?? '—'}</td>
                <td data-label="Duration">{entry.observation.durationMs} ms</td>
                <td data-label="Version">v{entry.observation.configVersion}</td>
                <td data-label="Initiated by">
                  {entry.observation.initiatedBy}
                  {entry.observation.counted ? null : (
                    <span className="not-counted">
                      {' '}
                      Not counted —{' '}
                      {notCountedReasonWords(entry.observation.notCountedReason ?? '')}
                    </span>
                  )}
                </td>
              </tr>
            ),
          )}
        </tbody>
      </table>
    </div>
  );
}

export default TimelineTable;
