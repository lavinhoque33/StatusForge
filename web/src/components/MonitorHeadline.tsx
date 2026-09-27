import type { Observation } from '../api/monitors';
import { observationReason } from '../lib/outcomes';
import { formatLocalDateTime, formatLocalWithOffset, formatRelativeAge } from '../lib/time';
import { OutcomeLabel } from './OutcomeLabel';

type MonitorHeadlineProps = {
  lastObservation: Observation | null;
  now: number;
};

/**
 * `Last manual check: <outcome> (<reason>) — <age> (<local time>)`, or
 * `Unknown — no checks yet`.
 *
 * There is no scheduler in M1, so the newest observation is all the interface
 * can honestly report; the panel never presents it as a current status.
 */
export function MonitorHeadline({ lastObservation, now }: MonitorHeadlineProps) {
  if (lastObservation === null) {
    return <p className="monitor-headline">Unknown — no checks yet</p>;
  }

  const { completedAt } = lastObservation;
  const title = formatLocalWithOffset(new Date(completedAt));
  return (
    <p className="monitor-headline">
      Last manual check: <OutcomeLabel observation={lastObservation} /> (
      {observationReason(lastObservation)}) —{' '}
      <time dateTime={completedAt} title={title}>
        {formatRelativeAge(completedAt, now)}
      </time>{' '}
      (
      <time dateTime={completedAt} title={title}>
        {formatLocalDateTime(new Date(completedAt))}
      </time>
      )
    </p>
  );
}

export default MonitorHeadline;
