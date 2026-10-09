import type { ReactNode } from 'react';

/** Chart colour family; `summary.css` maps each to a `--chart-*` token. */
export type SeriesTone =
  'healthy' | 'failing' | 'warning' | 'not-observed' | 'maintenance' | 'not-counted';

export type PatternSeries = { key: string; label: string; tone: SeriesTone };

const TILE = 6;

/**
 * One distinct texture per tone, so series stay distinguishable without
 * colour: calm texture (healthy), diagonal hatch (failing, missed), dots
 * (checker problem, late), cross-hatch (not observed), horizontal lines
 * (maintenance), vertical lines (not counted).
 */
const ink: Record<SeriesTone, { rotate: boolean; marks: ReactNode }> = {
  healthy: {
    rotate: false,
    marks: <circle cx={1.5} cy={1.5} r={0.75} className="chart-ink chart-ink--faint" />,
  },
  failing: {
    rotate: true,
    marks: <rect width={2.1} height={TILE} className="chart-ink" />,
  },
  warning: {
    rotate: false,
    marks: (
      <>
        <circle cx={1.5} cy={1.5} r={1.05} className="chart-ink" />
        <circle cx={4.5} cy={4.5} r={1.05} className="chart-ink" />
      </>
    ),
  },
  'not-observed': {
    rotate: true,
    marks: (
      <>
        <rect width={1.2} height={TILE} className="chart-ink" />
        <rect width={TILE} height={1.2} className="chart-ink" />
      </>
    ),
  },
  maintenance: {
    rotate: false,
    marks: <rect y={2.3} width={TILE} height={1.5} className="chart-ink" />,
  },
  'not-counted': {
    rotate: false,
    marks: <rect x={2.3} width={1.5} height={TILE} className="chart-ink" />,
  },
};

/**
 * Pattern definitions shared by the bars and the legend swatches of one
 * chart. Ids are `${prefix}-${series.key}`; the prefix comes from `useId`, so
 * two charts on one page never share a pattern.
 */
export function SeriesPatternDefs({
  prefix,
  series,
}: {
  prefix: string;
  series: readonly PatternSeries[];
}) {
  return (
    <svg className="chart-defs" width="0" height="0" aria-hidden="true" focusable="false">
      <defs>
        {series.map((item) => (
          <pattern
            key={item.key}
            id={`${prefix}-${item.key}`}
            patternUnits="userSpaceOnUse"
            width={TILE}
            height={TILE}
            patternTransform={ink[item.tone].rotate ? 'rotate(45)' : undefined}
          >
            <rect width={TILE} height={TILE} className={`chart-fill chart-fill--${item.tone}`} />
            {ink[item.tone].marks}
          </pattern>
        ))}
      </defs>
    </svg>
  );
}

export function PatternSwatch({ prefix, item }: { prefix: string; item: PatternSeries }) {
  return (
    <svg className="chart-swatch" width="14" height="14" aria-hidden="true" focusable="false">
      <rect
        className="chart-swatch__mark"
        x="0.5"
        y="0.5"
        width="13"
        height="13"
        rx="3.5"
        fill={`url(#${prefix}-${item.key})`}
      />
    </svg>
  );
}

/** HTML legend: the text label carries the meaning; the swatch repeats the bar pattern. */
export function SeriesLegend({
  prefix,
  series,
}: {
  prefix: string;
  series: readonly PatternSeries[];
}) {
  return (
    <ul className="chart-legend">
      {series.map((item) => (
        <li className="chart-legend__item" key={item.key}>
          <PatternSwatch prefix={prefix} item={item} />
          {item.label}
        </li>
      ))}
    </ul>
  );
}

export type LineSeries = { key: string; label: string; tone: 'median' | 'max' };

export function LineSwatch({ tone }: { tone: LineSeries['tone'] }) {
  return (
    <svg className="chart-swatch" width="18" height="14" aria-hidden="true" focusable="false">
      <line
        className={`chart-swatch__line chart-swatch__line--${tone}`}
        x1="2"
        x2="16"
        y1="7"
        y2="7"
      />
      <circle className={`chart-swatch__dot chart-swatch__dot--${tone}`} cx="9" cy="7" r="2.25" />
    </svg>
  );
}

export function LineLegend({ series }: { series: readonly LineSeries[] }) {
  return (
    <ul className="chart-legend">
      {series.map((item) => (
        <li className="chart-legend__item" key={item.key}>
          <LineSwatch tone={item.tone} />
          {item.label}
        </li>
      ))}
    </ul>
  );
}
