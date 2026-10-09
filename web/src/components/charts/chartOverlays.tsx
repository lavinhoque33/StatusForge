import type { ReactNode } from 'react';
import { DefaultZIndexes, usePlotArea, ZIndexLayer } from 'recharts';
import { layoutPills, PILL_HEIGHT, pillWidth, type Box } from '../../lib/bandLabels';
import type { ShadedSpan } from '../../lib/summaryPresentation';

type Tone = 'outage' | 'paused' | 'deadline';

function Pill({ box, text, tone }: { box: Box; text: string; tone: Tone }) {
  return (
    <g className={`chart-pill chart-pill--${tone}`}>
      <rect
        className="chart-pill__shape"
        x={box.x}
        y={box.y}
        width={box.width}
        height={box.height}
        rx={box.height / 2}
      />
      <text
        className="chart-pill__text"
        x={box.x + box.width / 2}
        y={box.y + box.height / 2}
        dominantBaseline="central"
        textAnchor="middle"
      >
        {text}
      </text>
    </g>
  );
}

const DEADLINE_TEXT = 'Check deadline';

/**
 * Pill on the left end of the check-deadline reference line, kept inside the
 * plot area. Recharts injects `viewBox` when it clones the element.
 */
export function DeadlineLabel({ viewBox }: { viewBox?: Box }) {
  const plot = usePlotArea();
  if (viewBox === undefined) return null;
  const width = pillWidth(DEADLINE_TEXT);
  const x =
    plot === undefined
      ? viewBox.x + 6
      : Math.max(plot.x + 2, Math.min(viewBox.x + 6, plot.x + plot.width - width - 2));
  return (
    <ZIndexLayer zIndex={DefaultZIndexes.label}>
      <Pill
        box={{ x, y: viewBox.y - PILL_HEIGHT / 2, width, height: PILL_HEIGHT }}
        text={DEADLINE_TEXT}
        tone="deadline"
      />
    </ZIndexLayer>
  );
}

/**
 * One label layer for every shaded band so all pills share a collision-aware
 * layout. `domain` is the time axis domain, which maps linearly onto the plot
 * width. `deadline` reserves the check-deadline pill as an obstacle.
 */
export function BandPills({
  spans,
  domain,
  deadline,
}: {
  spans: ShadedSpan[];
  domain: [number, number];
  deadline?: { ms: number; yMax: number };
}) {
  const plot = usePlotArea();
  if (plot === undefined || domain[1] <= domain[0]) return null;
  const scale = (value: number) =>
    plot.x +
    ((Math.max(domain[0], Math.min(value, domain[1])) - domain[0]) / (domain[1] - domain[0])) *
      plot.width;
  const labels = spans.map((span) => ({
    kind: span.kind,
    text: span.kind === 'outage' ? 'Outage' : 'Paused',
  }));
  const obstacles: Box[] = [];
  if (deadline !== undefined) {
    obstacles.push({
      x: plot.x + 6,
      y: plot.y + plot.height * (1 - deadline.ms / deadline.yMax) - PILL_HEIGHT / 2,
      width: pillWidth(DEADLINE_TEXT),
      height: PILL_HEIGHT,
    });
  }
  const boxes = layoutPills(
    spans.map((span, index) => ({
      center: (scale(span.from) + scale(span.to)) / 2,
      width: pillWidth(labels[index].text),
    })),
    { min: plot.x + 2, max: plot.x + plot.width - 2, top: plot.y },
    obstacles,
  );
  return (
    <ZIndexLayer zIndex={DefaultZIndexes.label}>
      {spans.map((span, index) => (
        <Pill
          key={`${span.kind}-${span.from}-${span.to}`}
          box={boxes[index]}
          text={labels[index].text}
          tone={span.kind}
        />
      ))}
    </ZIndexLayer>
  );
}

export type TooltipRow = { key: string; swatch: ReactNode; label: string; value: ReactNode };

/** Tooltip card shared by both charts: a title, optional facts, then one row per series. */
export function ChartTooltipCard({
  title,
  facts,
  rows,
}: {
  title: string;
  facts: { label: string; value: ReactNode }[];
  rows: TooltipRow[];
}) {
  return (
    <div className="chart-tooltip">
      <p className="chart-tooltip__title">{title}</p>
      {facts.length > 0 ? (
        <dl className="chart-tooltip__facts">
          {facts.map((fact) => (
            <div key={fact.label}>
              <dt>{fact.label}</dt>
              <dd>{fact.value}</dd>
            </div>
          ))}
        </dl>
      ) : null}
      <ul className="chart-tooltip__rows">
        {rows.map((row) => (
          <li key={row.key}>
            {row.swatch}
            <span className="chart-tooltip__label">{row.label}</span>
            <span className="chart-tooltip__value">{row.value}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
