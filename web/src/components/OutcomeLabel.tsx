import type { Observation } from '../api/monitors';
import { OUTCOME_GLYPHS, outcomeWord } from '../lib/outcomes';

/** Outcome word with a non-colour cue; colour is only an accent. */
export function OutcomeLabel({ observation }: { observation: Observation }) {
  return (
    <span className={`outcome outcome--${observation.outcome}`}>
      <span className="outcome-glyph" aria-hidden="true">
        {OUTCOME_GLYPHS[observation.outcome]}
      </span>{' '}
      <span className="outcome-word">{outcomeWord(observation.outcome)}</span>
    </span>
  );
}

export default OutcomeLabel;
