/**
 * Presented-status presentation: the headline per monitor,
 * the local switch to stale once `freshUntil` passes, and the state class that
 * colours the accent. Stale and unknown never use the healthy colour.
 */
import type { MonitorStatus, MonitorStatusEffective } from '../api/monitors';
import { intervalLabel } from './intervals';
import { observationReason, outcomeWord } from './outcomes';
import { formatLocalDateTime, formatLocalWithOffset, formatRelativeAge } from './time';

/**
 * Derive the state the interface presents: a fresh healthy/failing/
 * checker_problem status that passes `freshUntil` becomes `stale` locally,
 * without waiting for the next poll. Paused and archived are
 * lifecycle states and never go stale.
 */
export function effectiveState(status: MonitorStatus, now: number): MonitorStatusEffective {
  const outcomeState =
    status.state === 'healthy' || status.state === 'failing' || status.state === 'checker_problem';
  if (outcomeState && status.freshUntil !== null && Date.parse(status.freshUntil) < now) {
    return 'stale';
  }
  return status.state;
}

/**
 * `healthy`/`failing`/`checker_problem` → the outcome word (the state equals
 * the outcome); everything else uses its own word.
 */
function stateWord(state: MonitorStatusEffective): string {
  switch (state) {
    case 'healthy':
    case 'failing':
    case 'checker_problem':
      return outcomeWord(state);
    case 'stale':
      return 'Stale';
    case 'unknown':
      return 'Unknown';
    case 'paused':
      return 'Paused';
    case 'archived':
      return 'Archived';
    default: {
      const never: never = state;
      throw new Error(`unexpected state ${String(never)}`);
    }
  }
}

/** CSS accent class per state; colour is only an accent next to the words. */
export function stateClass(state: MonitorStatusEffective): string {
  return `monitor-state--${state}`;
}

type HeadlineTime = {
  /** The instant the `<time>` element carries. */
  instant: string;
  /** Local time text shown in the headline. */
  text: string;
  /** Hover title: local time with the UTC offset. */
  title: string;
};

type HeadlineParts = {
  /** The state word (`Stale`, `Unknown`, `Healthy`, …) with its cue. */
  word: string;
  /** Full headline words; `<time>` marks the local-time slot, if any. */
  text: string;
  /** The `<time>` slot to render inside the headline, when one is shown. */
  time: HeadlineTime | null;
};

/** `<Outcome> (<reason>) — checked <age> ago (<local time>), <scheduled|manual>` */
function outcomeHeadline(status: MonitorStatus, now: number): HeadlineParts {
  const observation = status.observation;
  if (observation === null) {
    // The contract ties these states to a counted observation; a missing one
    // is rendered as unknown rather than invented.
    return { word: 'Unknown', text: 'Unknown — no checks yet', time: null };
  }
  const word = stateWord(status.state);
  const reason = observationReason(observation);
  const completedAt = observation.completedAt;
  return {
    word,
    text:
      `${word} (${reason}) — checked ${formatRelativeAge(completedAt, now)} ` +
      `(<time>), ${observation.initiatedBy}`,
    time: {
      instant: completedAt,
      text: formatLocalDateTime(new Date(completedAt)),
      title: formatLocalWithOffset(new Date(completedAt)),
    },
  };
}

/** `Stale — last result <Outcome> <age> ago; expected every <interval>` */
function staleHeadline(status: MonitorStatus, now: number, intervalSeconds: number): HeadlineParts {
  const observation = status.observation;
  const last =
    observation === null
      ? ''
      : ` last result ${outcomeWord(observation.outcome)} ${formatRelativeAge(observation.completedAt, now)}`;
  return {
    word: 'Stale',
    text: `Stale —${last}; expected every ${intervalLabel(intervalSeconds)}`,
    time: null,
  };
}

/**
 * Headline words per state. `intervalSeconds` sizes the
 * stale headline's "expected every" phrase; the paused secondary text is
 * rendered by the component from `status.observation`.
 */
export function headlineParts(
  status: MonitorStatus,
  now: number,
  intervalSeconds: number,
): HeadlineParts {
  const state = effectiveState(status, now);
  switch (state) {
    case 'healthy':
    case 'failing':
    case 'checker_problem':
      return outcomeHeadline(status, now);
    case 'stale':
      return staleHeadline(status, now, intervalSeconds);
    case 'unknown':
      return status.reason === 'config_changed'
        ? {
            word: 'Unknown',
            text: 'Unknown — configuration changed, awaiting first check',
            time: null,
          }
        : { word: 'Unknown', text: 'Unknown — no checks yet', time: null };
    case 'paused':
      return { word: 'Paused', text: 'Paused', time: null };
    case 'archived':
      return { word: 'Archived', text: 'Archived', time: null };
    default: {
      const never: never = state;
      throw new Error(`unexpected state ${String(never)}`);
    }
  }
}
