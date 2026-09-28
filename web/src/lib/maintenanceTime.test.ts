import { describe, expect, it } from 'vitest';
import { localDateTimeToUtc, scheduleZoneLabel } from './maintenanceTime';

describe('maintenance local times', () => {
  it('converts local time using the browser zone and exposes that zone with its offset', () => {
    expect(localDateTimeToUtc('2026-09-27T12:30', 'Europe/Berlin')).toBe(
      '2026-09-27T10:30:00.000Z',
    );
    expect(scheduleZoneLabel(new Date('2026-09-27T10:00:00.000Z'), 'Europe/Berlin')).toBe(
      'Times in Europe/Berlin (UTC+02:00)',
    );
  });
  it('uses the offset after the DST spring change and rejects nonexistent or repeated wall times', () => {
    expect(localDateTimeToUtc('2026-03-29T03:30', 'Europe/Berlin')).toBe(
      '2026-03-29T01:30:00.000Z',
    );
    expect(() => localDateTimeToUtc('2026-03-29T02:30', 'Europe/Berlin')).toThrow(/does not exist/);
    expect(() => localDateTimeToUtc('2026-10-25T02:30', 'Europe/Berlin')).toThrow(/occurs twice/);
  });
});
