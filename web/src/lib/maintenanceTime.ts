import { formatLocalDateTime, formatOffset } from './time';

/** Date-time controls contain wall time without an offset; choose only a real, unambiguous instant. */
export function localDateTimeToUtc(
  value: string,
  timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone,
): string {
  if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d$/.test(value))
    throw new Error('Enter a complete local date and time.');
  const wall = Date.parse(`${value}:00.000Z`);
  if (Number.isNaN(wall)) throw new Error('Enter a valid local date and time.');
  // Offsets on both sides of a DST transition are considered; round-trip
  // checks reject skipped hours and avoid silently choosing a repeated hour.
  const offsets = new Set<number>();
  for (const delta of [-36, -12, 0, 12, 36]) {
    const offset = formatOffset(new Date(wall + delta * 3_600_000), timeZone);
    const sign = offset[0] === '-' ? -1 : 1;
    offsets.add(sign * (Number(offset.slice(1, 3)) * 60 + Number(offset.slice(4, 6))));
  }
  const matches = [...offsets]
    .map((minutes) => new Date(wall - minutes * 60_000))
    .filter((date) => formatLocalDateTime(date, timeZone).slice(0, 16).replace(' ', 'T') === value);
  if (matches.length !== 1) {
    throw new Error(
      matches.length === 0
        ? 'This local time does not exist in this time zone.'
        : 'This local time occurs twice. Choose another time.',
    );
  }
  return matches[0].toISOString();
}

export function scheduleZoneLabel(
  now = new Date(),
  timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone,
): string {
  return `Times in ${timeZone} (UTC${formatOffset(now, timeZone)})`;
}
