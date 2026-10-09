export type Box = { x: number; y: number; width: number; height: number };

/** Approximate advance of the 11 px semibold label face; keeps pills snug without measuring. */
const CHAR_WIDTH = 6.4;
export const PILL_HEIGHT = 18;
const ROW_GAP = 4;
const PILL_GAP = 4;
const TOP_OFFSET = 6;

export function pillWidth(text: string): number {
  return Math.round(text.length * CHAR_WIDTH + 16);
}

function overlaps(a: Box, b: Box): boolean {
  return (
    a.x < b.x + b.width + PILL_GAP &&
    b.x < a.x + a.width + PILL_GAP &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height
  );
}

/**
 * Place pills centred on their bands, clamped to `bounds`, then stack them in
 * rows: left to right, each pill takes the first row where it overlaps no
 * placed pill or obstacle. Row `r` sits `6 + r * (PILL_HEIGHT + 4)` px below `bounds.top`.
 * Returns boxes in input order.
 */
export function layoutPills(
  pills: { center: number; width: number }[],
  bounds: { min: number; max: number; top: number },
  obstacles: Box[] = [],
): Box[] {
  const placed: Box[] = [];
  const result: Box[] = new Array<Box>(pills.length);
  const order = pills.map((_, index) => index).sort((a, b) => pills[a].center - pills[b].center);
  for (const index of order) {
    const { center, width } = pills[index];
    const x = Math.max(bounds.min, Math.min(center - width / 2, bounds.max - width));
    for (let row = 0; ; row += 1) {
      const box: Box = {
        x,
        y: bounds.top + TOP_OFFSET + row * (PILL_HEIGHT + ROW_GAP),
        width,
        height: PILL_HEIGHT,
      };
      if (![...placed, ...obstacles].some((other) => overlaps(box, other))) {
        placed.push(box);
        result[index] = box;
        break;
      }
    }
  }
  return result;
}
