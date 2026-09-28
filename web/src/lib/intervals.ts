/**
 * Interval labels: the selector shows words, not seconds.
 * Unknown values fall back to a readable raw form.
 */
const INTERVAL_LABELS: Record<number, string> = {
  10: '10 s',
  15: '15 s',
  30: '30 s',
  60: '1 min',
  300: '5 min',
  600: '10 min',
  900: '15 min',
};

/** `60` → `1 min`, `300` → `5 min`, `45` → `45 s`. */
export function intervalLabel(intervalSeconds: number): string {
  const known = INTERVAL_LABELS[intervalSeconds];
  if (known !== undefined) return known;
  return `${intervalSeconds} s`;
}
