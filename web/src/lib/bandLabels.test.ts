import { describe, expect, it } from 'vitest';
import { layoutPills, PILL_HEIGHT, type Box } from './bandLabels';

const bounds = { min: 2, max: 602, top: 10 };
const rowY = (row: number) => bounds.top + 6 + row * (PILL_HEIGHT + 4);

function intersects(a: Box, b: Box): boolean {
  return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
}

describe('layoutPills', () => {
  it('keeps far-apart pills on the first row, centred on their bands', () => {
    const [left, right] = layoutPills(
      [
        { center: 100, width: 60 },
        { center: 400, width: 60 },
      ],
      bounds,
    );
    expect([left.y, right.y]).toEqual([rowY(0), rowY(0)]);
    expect(left.x).toBe(70);
  });

  it('moves a pill that overlaps an earlier one to the next row', () => {
    const boxes = layoutPills(
      [
        { center: 100, width: 60 },
        { center: 130, width: 60 },
      ],
      bounds,
    );
    expect(boxes.map((box) => box.y)).toEqual([rowY(0), rowY(1)]);
  });

  it('returns to the first row once a pill clears its pill', () => {
    const boxes = layoutPills(
      [
        { center: 100, width: 60 },
        { center: 130, width: 60 },
        { center: 300, width: 60 },
      ],
      bounds,
    );
    expect(boxes.map((box) => box.y)).toEqual([rowY(0), rowY(1), rowY(0)]);
  });

  it('keeps a pill clamped at the right edge clear of a pill about 55 px away', () => {
    const boxes = layoutPills(
      [
        { center: 547, width: 60 },
        { center: 600, width: 54 },
      ],
      bounds,
    );
    expect(boxes[1].x + boxes[1].width).toBeLessThanOrEqual(bounds.max);
    expect(intersects(boxes[0], boxes[1])).toBe(false);
  });

  it('moves a pill that overlaps an obstacle down', () => {
    const obstacle: Box = { x: 8, y: rowY(0), width: 100, height: PILL_HEIGHT };
    const [box] = layoutPills([{ center: 60, width: 60 }], bounds, [obstacle]);
    expect(box.y).toBe(rowY(1));
    expect(intersects(box, obstacle)).toBe(false);
  });
});
