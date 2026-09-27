import { describe, expect, it } from 'vitest';
import {
  formatDeadlineSeconds,
  formatLocalDateTime,
  formatLocalWithOffset,
  formatOffset,
  formatRelativeAge,
} from './time';

describe('local time formatting', () => {
  it('formats an instant in the requested zone', () => {
    const instant = new Date('2026-09-27T10:15:30.123Z');
    expect(formatLocalDateTime(instant, 'UTC')).toBe('2026-09-27 10:15:30');
    expect(formatLocalDateTime(instant, 'Europe/Berlin')).toBe('2026-09-27 12:15:30');
  });

  it('exposes the UTC offset for both summer and winter instants', () => {
    expect(formatOffset(new Date('2026-09-27T10:15:30.123Z'), 'UTC')).toBe('+00:00');
    expect(formatOffset(new Date('2026-09-27T10:15:30.123Z'), 'Europe/Berlin')).toBe('+02:00');
    expect(formatOffset(new Date('2026-01-15T10:15:30.000Z'), 'Europe/Berlin')).toBe('+01:00');
    expect(formatOffset(new Date('2026-01-15T10:15:30.000Z'), 'America/New_York')).toBe('-05:00');
  });

  it('combines local time and offset for the table', () => {
    expect(formatLocalWithOffset(new Date('2026-01-15T10:15:30.000Z'), 'Europe/Berlin')).toBe(
      '2026-01-15 11:15:30 +01:00',
    );
  });
});

describe('formatDeadlineSeconds', () => {
  it.each([
    [1000, '1 s'],
    [10000, '10 s'],
    [30000, '30 s'],
    [1500, '1.5 s'],
  ])('renders %i ms as %s', (deadlineMs, text) => {
    expect(formatDeadlineSeconds(deadlineMs)).toBe(text);
  });
});

describe('formatRelativeAge', () => {
  const now = Date.parse('2026-09-27T10:20:00.000Z');

  it.each([
    ['2026-09-27T10:19:57.000Z', 'just now'],
    ['2026-09-27T10:19:18.000Z', '42 s ago'],
    ['2026-09-27T10:18:00.000Z', '2 min ago'],
    ['2026-09-27T07:20:00.000Z', '3 h ago'],
    ['2026-09-26T10:20:00.000Z', '1 d ago'],
  ])('renders %s as %s', (instant, text) => {
    expect(formatRelativeAge(instant, now)).toBe(text);
  });

  it('never reports a negative age when the client clock lags the server', () => {
    expect(formatRelativeAge('2026-09-27T10:25:00.000Z', now)).toBe('just now');
  });
});
