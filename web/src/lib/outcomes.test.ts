import { describe, expect, it } from 'vitest';
import type { CheckOutcome } from '../api/monitors';
import { checkFixture, observationFixture } from '../test/fixtures';
import { observationReason, outcomeWord } from './outcomes';

describe('outcomeWord', () => {
  it.each([
    ['healthy', 'Healthy'],
    ['failing', 'Failing'],
    ['checker_problem', 'Checker problem'],
  ] as [CheckOutcome, string][])('renders %s as %s', (outcome, word) => {
    expect(outcomeWord(outcome)).toBe(word);
  });
});

describe('observationReason', () => {
  it('states the status the target returned and the status that was expected', () => {
    const observation = observationFixture({
      outcome: 'failing',
      reason: 'wrong_status',
      observedStatus: 500,
      request: checkFixture({ expectedStatus: 200 }),
    });
    expect(observationReason(observation)).toBe('wrong status: got 500, expected 200');
  });

  it('names the deadline a timed out check ran under', () => {
    const observation = observationFixture({
      outcome: 'failing',
      reason: 'timeout',
      request: checkFixture({ deadlineMs: 1000 }),
    });
    expect(observationReason(observation)).toBe('timed out after 1 s');
  });

  it.each([
    ['ok', 'ok'],
    ['connection_refused', 'connection refused'],
    ['connection_error', 'connection error'],
    ['refused_by_policy', 'target refused by policy'],
    ['internal', 'internal error'],
  ])('renders %s as %s', (reason, text) => {
    expect(observationReason(observationFixture({ reason }))).toBe(text);
  });

  it('shows an unfamiliar reason readably instead of reclassifying it', () => {
    expect(observationReason(observationFixture({ reason: 'upstream_confusion' }))).toBe(
      'upstream confusion',
    );
  });
});
