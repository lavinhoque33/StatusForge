/**
 * Time presentation: absolute times in the reader's local zone
 * with the UTC offset available, and relative ages computed on the client.
 *
 * `timeZone` is an optional injection point for tests; leaving it out uses the
 * browser's zone.
 */

const MS_PER_SECOND = 1000;
const dateTimeFormatters = new Map<string, Intl.DateTimeFormat>();
const offsetFormatters = new Map<string, Intl.DateTimeFormat>();

function dateTimeFormatter(timeZone: string | undefined): Intl.DateTimeFormat {
  const key = timeZone ?? 'local';
  const cached = dateTimeFormatters.get(key);
  if (cached !== undefined) return cached;
  const formatter = new Intl.DateTimeFormat('en-CA', {
    ...(timeZone === undefined ? {} : { timeZone }),
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  });
  dateTimeFormatters.set(key, formatter);
  return formatter;
}

function offsetFormatter(timeZone: string | undefined): Intl.DateTimeFormat {
  const key = timeZone ?? 'local';
  const cached = offsetFormatters.get(key);
  if (cached !== undefined) return cached;
  const formatter = new Intl.DateTimeFormat('en-US', {
    ...(timeZone === undefined ? {} : { timeZone }),
    timeZoneName: 'longOffset',
  });
  offsetFormatters.set(key, formatter);
  return formatter;
}

function part(parts: Intl.DateTimeFormatPart[], type: Intl.DateTimeFormatPartTypes): string {
  return parts.find((entry) => entry.type === type)?.value ?? '';
}

/** `2026-09-27 10:15:30` in the given zone (local when omitted). */
export function formatLocalDateTime(date: Date, timeZone?: string): string {
  if (Number.isNaN(date.getTime())) return 'unknown time';
  const parts = dateTimeFormatter(timeZone).formatToParts(date);
  return (
    `${part(parts, 'year')}-${part(parts, 'month')}-${part(parts, 'day')} ` +
    `${part(parts, 'hour')}:${part(parts, 'minute')}:${part(parts, 'second')}`
  );
}

/** `+02:00` / `-05:00` / `+00:00` for the given instant and zone. */
export function formatOffset(date: Date, timeZone?: string): string {
  if (Number.isNaN(date.getTime())) return '+00:00';
  const name = part(offsetFormatter(timeZone).formatToParts(date), 'timeZoneName');
  const match = /^(?:GMT|UTC)([+-])(\d{1,2})(?::(\d{2}))?$/.exec(name);
  if (match === null) return '+00:00';
  const [, sign, hours, minutes] = match;
  return `${sign}${hours.padStart(2, '0')}:${minutes ?? '00'}`;
}

/** `2026-09-27 10:15:30 +02:00` in the given zone (local when omitted). */
export function formatLocalWithOffset(date: Date, timeZone?: string): string {
  return `${formatLocalDateTime(date, timeZone)} ${formatOffset(date, timeZone)}`;
}

/** `1 s`, `10 s`, `1.5 s` — the deadline as the check summary states it. */
export function formatDeadlineSeconds(deadlineMs: number): string {
  const seconds = deadlineMs / MS_PER_SECOND;
  return `${Number.isInteger(seconds) ? String(seconds) : seconds.toFixed(1)} s`;
}

/**
 * `just now`, `42 s ago`, `3 min ago`, `2 h ago`, `1 d ago`.
 *
 * Ages are computed on the client and refresh on a timer; a clock that runs
 * behind the server never produces a negative age.
 */
export function formatRelativeAge(instant: string, now: number): string {
  const time = Date.parse(instant);
  if (Number.isNaN(time)) return 'unknown age';
  const seconds = Math.floor(Math.max(0, now - time) / MS_PER_SECOND);
  if (seconds < 5) return 'just now';
  if (seconds < 60) return `${seconds} s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}
