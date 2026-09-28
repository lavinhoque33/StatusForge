/**
 * Gap and not-counted reason vocabulary: plain-language words
 * for why checks were missed and why a stored observation did not count.
 * Unknown codes stay readable rather than hidden or reclassified, so a new
 * backend reason cannot silently change the meaning of an entry.
 */

const GAP_REASONS: Record<string, string> = {
  not_scheduled: 'StatusForge was not running',
  overdue: 'workers were busy',
  lease_expired: 'a check was interrupted',
};

const NOT_COUNTED_REASONS: Record<string, string> = {
  paused: 'monitor was paused',
  archived: 'monitor was archived',
  config_changed: 'configuration changed during the check',
  older_than_current: 'a newer result already existed',
  lease_lost: 'the check outlived its lease',
};

/** `not_scheduled` → `StatusForge was not running`. */
export function gapReasonWords(reason: string): string {
  return GAP_REASONS[reason] ?? reason.split('_').join(' ');
}

/** `paused` → `monitor was paused`. */
export function notCountedReasonWords(reason: string): string {
  return NOT_COUNTED_REASONS[reason] ?? reason.split('_').join(' ');
}
