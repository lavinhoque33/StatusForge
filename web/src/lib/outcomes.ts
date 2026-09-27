/**
 * Outcome vocabulary: an outcome word, a plain-language reason,
 * and a non-colour cue. Colour is only an accent in the stylesheet.
 */
import type { CheckOutcome, Observation } from '../api/monitors';
import { formatDeadlineSeconds } from './time';

const OUTCOME_WORDS: Record<CheckOutcome, string> = {
  healthy: 'Healthy',
  failing: 'Failing',
  checker_problem: 'Checker problem',
};

/** Non-colour cue rendered next to the word (hidden from assistive tech). */
export const OUTCOME_GLYPHS: Record<CheckOutcome, string> = {
  healthy: '✔',
  failing: '✖',
  checker_problem: '⚠',
};

/** `healthy` → `Healthy`, `checker_problem` → `Checker problem`. */
export function outcomeWord(outcome: CheckOutcome): string {
  return OUTCOME_WORDS[outcome];
}

/**
 * Plain-language reason for an observation.
 *
 * Known codes are stated as words; an unknown code is shown readably rather
 * than hidden or reclassified, so a new backend reason cannot silently change
 * the meaning of an existing observation.
 */
export function observationReason(observation: Observation): string {
  switch (observation.reason) {
    case 'ok':
      return 'ok';
    case 'wrong_status':
      return observation.observedStatus === undefined
        ? `wrong status: expected ${observation.request.expectedStatus}`
        : `wrong status: got ${observation.observedStatus}, expected ${observation.request.expectedStatus}`;
    case 'timeout':
      return `timed out after ${formatDeadlineSeconds(observation.request.deadlineMs)}`;
    case 'connection_refused':
      return 'connection refused';
    case 'connection_error':
      return 'connection error';
    case 'refused_by_policy':
      return 'target refused by policy';
    case 'internal':
      return 'internal error';
    default:
      return observation.reason.split('_').join(' ');
  }
}
