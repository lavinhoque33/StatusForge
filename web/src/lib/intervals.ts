/**
 * Interval labels: the selector shows words, not seconds.
 * Unknown values fall back to a readable raw form.
 */
const INTERVAL_LABELS: Record<number, string> = {
  10: '10 s',
  15: '15 s',
  30: '30 s',
  5: '5 s',
  60: '1 min',
  300: '5 min',
  600: '10 min',
  900: '15 min',
  1800: '30 min',
  3600: '1 h',
  21600: '6 h',
  43200: '12 h',
  86400: '24 h',
  604800: '7 d',
};

/** `60` → `1 min`, `300` → `5 min`, `45` → `45 s`. */
export function intervalLabel(intervalSeconds: number): string {
  const known = INTERVAL_LABELS[intervalSeconds];
  if (known !== undefined) return known;
  return `${intervalSeconds} s`;
}
