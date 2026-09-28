import type { ReactNode } from 'react';
import type { MonitorStatus } from '../api/monitors';
import { outcomeWord } from '../lib/outcomes';
import { effectiveState, headlineParts, stateClass } from '../lib/statusHeadline';
import { formatRelativeAge } from '../lib/time';

type MonitorHeadlineProps = {
  status: MonitorStatus;
  intervalSeconds: number;
  now: number;
};

/** Render `text` with its single `<time>` slot replaced by the real element. */
function renderWithTime(text: string, time: NonNullable<HeadlineTimeLike>): ReactNode {
  const [before, after] = text.split('<time>');
  if (after === undefined) return text;
  return (
    <>
      {before}
      <time dateTime={time.instant} title={time.title}>
        {time.text}
      </time>
      {after}
    </>
  );
}

/** The subset of `HeadlineParts['time']` the renderer needs. */
type HeadlineTimeLike = {
  instant: string;
  text: string;
  title: string;
};

/**
 * The headline per monitor: the presented state in words with
 * the observation it rests on, or the lifecycle state. Colour is an accent via
 * the state class; the words always carry the meaning, and stale/unknown never
 * receive the healthy accent.
 */
export function MonitorHeadline({ status, intervalSeconds, now }: MonitorHeadlineProps) {
  const state = effectiveState(status, now);
  const parts = headlineParts(status, now, intervalSeconds);

  if (state === 'paused' && status.observation !== null) {
    const completedAt = status.observation.completedAt;
    return (
      <>
        <p className={`monitor-headline ${stateClass(state)}`}>{parts.text}</p>
        <p className="monitor-headline-secondary">
          last result {outcomeWord(status.observation.outcome)}{' '}
          {formatRelativeAge(completedAt, now)}
        </p>
      </>
    );
  }

  return (
    <p className={`monitor-headline ${stateClass(state)}`}>
      {parts.time === null ? parts.text : renderWithTime(parts.text, parts.time)}
    </p>
  );
}

export default MonitorHeadline;
