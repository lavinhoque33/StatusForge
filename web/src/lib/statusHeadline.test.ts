import { describe, expect, it } from 'vitest';
import { monitorStatusFixture, observationFixture } from '../test/fixtures';
import { effectiveState, headlineParts, stateClass } from './statusHeadline';
import { intervalLabel } from './intervals';
import { updatedText } from './refresh';

const NOW = Date.parse('2026-09-27T12:00:00.000Z');

describe('effectiveState', () => {
  it('keeps a fresh outcome state as presented', () => {
    const status = monitorStatusFixture({
      state: 'healthy',
      freshUntil: '2026-09-27T12:00:01.000Z',
    });
    expect(effectiveState(status, NOW)).toBe('healthy');
  });

  it('switches to stale locally once freshUntil passes, without a poll', () => {
    const status = monitorStatusFixture({
      state: 'healthy',
      freshUntil: '2026-09-27T11:59:59.000Z',
    });
    expect(effectiveState(status, NOW)).toBe('stale');
  });

  it('never makes paused or archived stale', () => {
    expect(effectiveState(monitorStatusFixture({ state: 'paused', freshUntil: null }), NOW)).toBe(
      'paused',
    );
    expect(effectiveState(monitorStatusFixture({ state: 'archived', freshUntil: null }), NOW)).toBe(
      'archived',
    );
  });
});

describe('headlineParts', () => {
  it('renders the outcome headline with reason, age, local time, and initiator', () => {
    const status = monitorStatusFixture({
      state: 'failing',
      observation: observationFixture({
        outcome: 'failing',
        reason: 'connection_refused',
        completedAt: '2026-09-27T11:59:40.000Z',
        startedAt: '2026-09-27T11:59:40.000Z',
        initiatedBy: 'scheduled',
      }),
      freshUntil: '2026-09-27T12:30:00.000Z',
    });

    const parts = headlineParts(status, NOW, 60);
    expect(parts.text).toBe('Failing (connection refused) — checked 20 s ago (<time>), scheduled');
  });

  it('renders the outcome reason in plain language, not with split underscores', () => {
    const status = monitorStatusFixture({
      state: 'failing',
      observation: observationFixture({
        outcome: 'failing',
        reason: 'wrong_status',
        observedStatus: 500,
      }),
      freshUntil: '2026-09-27T12:30:00.000Z',
    });

    const parts = headlineParts(status, NOW, 60);
    expect(parts.text).toContain('wrong status: got 500, expected 200');
    expect(parts.text).not.toContain('wrong status got 500');
  });

  it('renders the stale headline with the last result and the expected cadence', () => {
    const status = monitorStatusFixture({
      state: 'healthy',
      observation: observationFixture({
        outcome: 'healthy',
        completedAt: '2026-09-27T10:00:00.000Z',
      }),
      freshUntil: '2026-09-27T10:20:00.000Z',
    });

    const parts = headlineParts(status, NOW, 300);
    expect(parts.word).toBe('Stale');
    expect(parts.text).toBe('Stale — last result Healthy 2 h ago; expected every 5 min');
  });

  it('renders both unknown reasons with the contract wording', () => {
    expect(
      headlineParts(monitorStatusFixture({ state: 'unknown', reason: 'no_checks' }), NOW, 60).text,
    ).toBe('Unknown — no checks yet');
    expect(
      headlineParts(monitorStatusFixture({ state: 'unknown', reason: 'config_changed' }), NOW, 60)
        .text,
    ).toBe('Unknown — configuration changed, awaiting first check');
  });

  it('renders paused and archived as their own states', () => {
    expect(headlineParts(monitorStatusFixture({ state: 'paused' }), NOW, 60).text).toBe('Paused');
    expect(headlineParts(monitorStatusFixture({ state: 'archived' }), NOW, 60).text).toBe(
      'Archived',
    );
  });

  it('maps the state to an accent class that stale and unknown never share with healthy', () => {
    expect(stateClass('healthy')).toBe('monitor-state--healthy');
    expect(stateClass('stale')).toBe('monitor-state--stale');
    expect(stateClass('unknown')).toBe('monitor-state--unknown');
    expect(stateClass('stale')).not.toBe(stateClass('healthy'));
  });
});

describe('intervalLabel', () => {
  it.each([
    [10, '10 s'],
    [15, '15 s'],
    [30, '30 s'],
    [60, '1 min'],
    [300, '5 min'],
    [600, '10 min'],
    [900, '15 min'],
  ] as [number, string][])('labels %i s as %s', (seconds, label) => {
    expect(intervalLabel(seconds)).toBe(label);
  });
});

describe('updatedText', () => {
  it('states how long ago the last poll succeeded', () => {
    expect(updatedText(NOW - 42_000, NOW)).toBe('Updated 42 s ago');
    expect(updatedText(NOW - 1_000, NOW)).toBe('Updated just now');
  });
});
