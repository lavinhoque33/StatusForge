/**
 * `Updated <N> s ago` and the not-updated marker.
 */
import { formatRelativeAge } from './time';

/** `0` → `Updated just now`, `42` → `Updated 42 s ago`. */
export function updatedText(lastUpdatedMs: number, now: number): string {
  return `Updated ${formatRelativeAge(new Date(lastUpdatedMs).toISOString(), now)}`;
}

/** Reason words for a failed poll; the marker keeps the last data visible. */
export function notUpdatedText(reason: string): string {
  return `Not updated — ${reason}`;
}
