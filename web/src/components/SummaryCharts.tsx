import { useId, useState, useSyncExternalStore } from 'react';
import {
  Area,
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceArea,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type BarShapeProps,
  type DotItemDotProps,
} from 'recharts';
import type { HttpSummary, MonitorSummary, Span } from '../api/dailyUse';
import {
  chartRows,
  heartbeatChartRows,
  latencyAxisMaximum,
  latencyPlotRows,
  latencyTicks,
  latencyValue,
  shadedSpans,
  statusBucketLabel,
  type ChartRow,
} from '../lib/summaryPresentation';
import { formatLocalWithOffset } from '../lib/time';
import { BandPills, ChartTooltipCard, DeadlineLabel } from './charts/chartOverlays';
import {
  LineLegend,
  LineSwatch,
  PatternSwatch,
  SeriesLegend,
  SeriesPatternDefs,
  type LineSeries,
  type PatternSeries,
} from './charts/seriesPatterns';
import { Icon } from './Icon';
import { ScrollRegion } from './ScrollRegion';

const httpSeries = [
  { key: 'healthy', label: 'Healthy', tone: 'healthy' },
  { key: 'failing', label: 'Failing', tone: 'failing' },
  { key: 'checkerProblem', label: 'Checker problem', tone: 'warning' },
  { key: 'notObserved', label: 'Not observed', tone: 'not-observed' },
  { key: 'maintenance', label: 'Maintenance', tone: 'maintenance' },
  { key: 'notCounted', label: 'Not counted', tone: 'not-counted' },
] as const satisfies readonly PatternSeries[];
const heartbeatSeries = [
  { key: 'onTime', label: 'On time', tone: 'healthy' },
  { key: 'late', label: 'Late', tone: 'warning' },
  { key: 'missed', label: 'Missed', tone: 'failing' },
  { key: 'notObserved', label: 'Not observed', tone: 'not-observed' },
  { key: 'maintenance', label: 'Maintenance', tone: 'maintenance' },
  { key: 'notCounted', label: 'Not counted', tone: 'not-counted' },
] as const satisfies readonly PatternSeries[];
const latencySeries = [
  { key: 'medianMs', label: 'Median response time', tone: 'median' },
  { key: 'maxMs', label: 'Maximum response time', tone: 'max' },
] as const satisfies readonly LineSeries[];
const statusRows = (summary: MonitorSummary) =>
  summary.kind === 'http'
    ? chartRows(summary).map((row) => ({
        ...row,
        onTime: 0,
        late: 0,
        missed: 0,
        failureReports: 0,
      }))
    : heartbeatChartRows(summary).map((row) => ({
        ...row,
        healthy: 0,
        failing: 0,
        checkerProblem: 0,
        samples: 0,
        medianMs: null as number | null,
        maxMs: null as number | null,
      }));
const statusSeries = (summary: MonitorSummary) =>
  summary.kind === 'http' ? httpSeries : heartbeatSeries;

/** Chart sizing and labels follow the viewport; jsdom has no `matchMedia`. */
function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const list = window.matchMedia?.(query);
      list?.addEventListener('change', onChange);
      return () => list?.removeEventListener('change', onChange);
    },
    () => window.matchMedia?.(query).matches ?? false,
  );
}
/** `useId` output made safe for `url(#…)` references. */
function useSvgId(): string {
  return `sf${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`;
}

function timeTick(value: number) {
  return new Date(value).toLocaleDateString(undefined, {
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
  });
}
const tick = { fontSize: 11 };
function ChartToggle({ table, onToggle }: { table: boolean; onToggle: () => void }) {
  return (
    <button className="button summary-toggle" type="button" aria-pressed={table} onClick={onToggle}>
      <Icon name={table ? 'chart' : 'table'} size={16} />
      {table ? 'Show as chart' : 'Show as table'}
    </button>
  );
}
function ChartTable({
  summary,
  latency,
  deadlineMs,
}: {
  summary: MonitorSummary;
  latency: boolean;
  deadlineMs?: number;
}) {
  const captionId = useId();
  return (
    <ScrollRegion labelledBy={captionId}>
      <table className="observations summary-table">
        <caption id={captionId}>
          {latency ? (
            <>
              Response time by bucket — {summary.window}, {summary.bucketSeconds / 3600} h buckets.
              Denominator: responses within the check deadline ({deadlineMs} ms); no-response and
              checker problems excluded. Empty buckets have no line.{' '}
            </>
          ) : (
            <>
              Status history by bucket — {summary.window}, {summary.bucketSeconds / 3600} h buckets.
              Denominator: expected{' '}
              {summary.kind === 'http' ? 'scheduled checks' : 'heartbeat deadlines'}; paused time
              excluded.{' '}
            </>
          )}
          {summary.truncated
            ? `Partial window from ${formatLocalWithOffset(new Date(summary.coveredFrom))}.`
            : latency
              ? 'Measured from this machine, not user experience.'
              : 'Only recorded observations and explicit gaps are counted.'}
        </caption>
        <thead>
          <tr>
            <th scope="col">Bucket (local)</th>
            {latency ? (
              <>
                <th scope="col">Responses</th>
                <th scope="col">Median</th>
                <th scope="col">Max</th>
              </>
            ) : (
              <>
                <th scope="col">Expected</th>
                <th scope="col">Recorded</th>
                {statusSeries(summary).map((item) => (
                  <th scope="col" key={item.key}>
                    {item.label}
                  </th>
                ))}
                {summary.kind === 'heartbeat' ? (
                  <th scope="col">Failure reports (subset)</th>
                ) : null}
                <th scope="col">Paused seconds</th>
                <th scope="col">Receive outage</th>
              </>
            )}
          </tr>
        </thead>
        <tbody>
          {statusRows(summary).map((row) => (
            <tr key={row.from}>
              <th scope="row">
                {formatLocalWithOffset(new Date(row.from))} –{' '}
                {formatLocalWithOffset(new Date(row.to))}
              </th>
              {latency ? (
                <>
                  <td data-label="Responses">{row.samples}</td>
                  <td data-label="Median">
                    {row.medianMs === null ? 'No median' : `${row.medianMs} ms`}
                  </td>
                  <td data-label="Max">{row.maxMs === null ? 'No response' : `${row.maxMs} ms`}</td>
                </>
              ) : (
                <>
                  <td data-label="Expected">{row.expected}</td>
                  <td data-label="Recorded">{row.recorded}</td>
                  {statusSeries(summary).map((item) => (
                    <td data-label={item.label} key={item.key}>
                      {row[item.key]}
                    </td>
                  ))}
                  {summary.kind === 'heartbeat' ? (
                    <td data-label="Failure reports (subset)">{row.failureReports}</td>
                  ) : null}
                  <td data-label="Paused seconds">{row.pausedSeconds}</td>
                  <td data-label="Receive outage">
                    {summary.outages.some(
                      (span: Span) =>
                        Date.parse(span.from) < Date.parse(row.to) &&
                        Date.parse(span.to ?? summary.to) > Date.parse(row.from),
                    )
                      ? 'StatusForge was not receiving'
                      : '—'}
                  </td>
                </>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </ScrollRegion>
  );
}
/** Stacked segment with a hairline gap above it, so buckets read as discrete counts. */
function StatusSegment({ x, y, width, height, fill }: BarShapeProps) {
  if (!(height > 0) || !(width > 0)) return <g />;
  const gap = Math.min(1.5, height / 3);
  return (
    <rect
      className="chart-segment"
      x={x}
      y={y + gap}
      width={width}
      height={height - gap}
      rx={Math.min(2.5, width / 4, (height - gap) / 2)}
      fill={fill}
    />
  );
}
function HistoryChart({
  summary,
  reducedMotion,
  wide,
}: {
  summary: MonitorSummary;
  reducedMotion: boolean;
  wide: boolean;
}) {
  const [table, setTable] = useState(false);
  const rows = statusRows(summary);
  // Paused bands first so receive outages paint on top of them.
  const bands = shadedSpans(summary, rows, true).sort(
    (left, right) => Number(left.kind === 'outage') - Number(right.kind === 'outage'),
  );
  const series = statusSeries(summary);
  const captionId = useId();
  const patternPrefix = useSvgId();
  return (
    <figure className="summary-figure">
      <div className="summary-figure__header">
        <figcaption id={captionId} className="summary-figure__caption">
          <span className="summary-figure__title">Status history</span>
          <span className="summary-figure__description">
            <span className="summary-figure__separator"> — </span>
            {`${summary.window}, ${summary.bucketSeconds / 3600} h buckets. Denominator: expected ${summary.kind === 'http' ? 'scheduled checks' : 'heartbeat deadlines'}; paused time excluded.`}
            {summary.truncated
              ? ` Partial window from ${formatLocalWithOffset(new Date(summary.coveredFrom))}.`
              : ' Unobserved slots are not treated as healthy or failing.'}
          </span>
        </figcaption>
        <ChartToggle table={table} onToggle={() => setTable(!table)} />
      </div>
      <p className="summary-shading-key">
        Shaded spans: <span className="shade-key--outage">Receive outage</span> — StatusForge was
        not receiving; <span className="shade-key--paused">Paused</span> — monitoring paused.
      </p>
      {table ? (
        <ChartTable summary={summary} latency={false} />
      ) : (
        <>
          <SeriesPatternDefs prefix={patternPrefix} series={series} />
          <SeriesLegend prefix={patternPrefix} series={series} />
          <div className="summary-plot">
            <ResponsiveContainer width="100%" height={wide ? 280 : 232}>
              <ComposedChart
                data={rows}
                accessibilityLayer
                aria-labelledby={captionId}
                barCategoryGap={wide ? '24%' : '16%'}
                margin={{ top: 8, right: 8, bottom: 0, left: 0 }}
              >
                <CartesianGrid xAxisId="buckets" vertical={false} />
                <XAxis
                  xAxisId="buckets"
                  dataKey="at"
                  tickFormatter={timeTick}
                  interval="preserveStartEnd"
                  minTickGap={18}
                  tickMargin={8}
                  axisLine={false}
                  tickLine={false}
                  tick={tick}
                />
                <XAxis
                  xAxisId="time"
                  type="number"
                  dataKey="at"
                  hide
                  domain={[Date.parse(summary.from), Date.parse(summary.to)]}
                />
                <YAxis
                  allowDecimals={false}
                  axisLine={false}
                  tickLine={false}
                  tick={tick}
                  tickMargin={6}
                  width={36}
                />
                <Tooltip
                  axisId="buckets"
                  cursor={{ className: 'chart-cursor' }}
                  isAnimationActive={!reducedMotion}
                  content={({ active, payload }) => {
                    if (!active || !payload?.length) return null;
                    const row = payload[0].payload as (typeof rows)[number];
                    return (
                      <ChartTooltipCard
                        title={statusBucketLabel(rows, row.at)}
                        facts={[
                          { label: 'Expected', value: row.expected },
                          { label: 'Recorded', value: row.recorded },
                        ]}
                        rows={series.map((item) => ({
                          key: item.key,
                          swatch: <PatternSwatch prefix={patternPrefix} item={item} />,
                          label: item.label,
                          value: row[item.key],
                        }))}
                      />
                    );
                  }}
                />
                {bands.map((area) => (
                  <ReferenceArea
                    key={`${area.kind}-${area.from}-${area.to}`}
                    xAxisId="time"
                    x1={area.from}
                    x2={area.to}
                    className={`chart-band chart-band--${area.kind}`}
                  />
                ))}
                {wide ? (
                  <BandPills
                    spans={bands}
                    domain={[Date.parse(summary.from), Date.parse(summary.to)]}
                  />
                ) : null}
                {series.map((item) => (
                  <Bar
                    xAxisId="buckets"
                    key={item.key}
                    dataKey={item.key}
                    name={item.label}
                    stackId="slots"
                    className={`chart-bar chart-bar--${item.tone}`}
                    fill={`url(#${patternPrefix}-${item.key})`}
                    maxBarSize={28}
                    shape={StatusSegment}
                    isAnimationActive={!reducedMotion}
                  />
                ))}
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </>
      )}
    </figure>
  );
}
function LatencyChart({
  summary,
  deadlineMs,
  reducedMotion,
  wide,
}: {
  summary: HttpSummary;
  deadlineMs: number;
  reducedMotion: boolean;
  wide: boolean;
}) {
  const [table, setTable] = useState(false);
  const rows = latencyPlotRows(summary);
  const captionId = useId();
  const gradientId = useSvgId();
  const yTicks = latencyTicks(latencyAxisMaximum(summary, deadlineMs));
  const outageSpans = shadedSpans(summary, chartRows(summary), false);
  /** A dot only where neither neighbour has a value, so an isolated bucket is never invisible. */
  const isolatedDot =
    (key: 'medianMs' | 'maxMs') =>
    ({ index, cx, cy }: DotItemDotProps) => {
      if (rows[index]?.[key] == null || cx == null || cy == null) return null;
      if (rows[index - 1]?.[key] != null || rows[index + 1]?.[key] != null) return null;
      return (
        <circle
          className={`chart-dot chart-dot--${key === 'medianMs' ? 'median' : 'max'}`}
          cx={cx}
          cy={cy}
          r={key === 'medianMs' ? 3 : 2.5}
        />
      );
    };
  return (
    <figure className="summary-figure">
      <div className="summary-figure__header">
        <figcaption id={captionId} className="summary-figure__caption">
          <span className="summary-figure__title">Response time</span>
          <span className="summary-figure__description">
            <span className="summary-figure__separator"> — </span>
            {`${summary.window}, ${summary.bucketSeconds / 3600} h buckets. Denominator: responses within the check deadline (${deadlineMs} ms); no-response and checker problems excluded. Empty buckets have no line. `}
            {summary.truncated
              ? `Partial window from ${formatLocalWithOffset(new Date(summary.coveredFrom))}.`
              : 'Measured from this machine, not user experience.'}
          </span>
        </figcaption>
        <ChartToggle table={table} onToggle={() => setTable(!table)} />
      </div>
      <p className="summary-shading-key">
        Shaded spans: <span className="shade-key--outage">Receive outage</span> — StatusForge was
        not receiving.
      </p>
      {table ? (
        <ChartTable summary={summary} latency deadlineMs={deadlineMs} />
      ) : (
        <>
          <LineLegend series={latencySeries} />
          <div className="summary-plot">
            <ResponsiveContainer width="100%" height={wide ? 280 : 232}>
              <ComposedChart
                data={rows}
                accessibilityLayer
                aria-labelledby={captionId}
                margin={{ top: 12, right: 12, bottom: 0, left: 0 }}
              >
                <defs>
                  <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" className="chart-gradient-stop chart-gradient-stop--top" />
                    <stop
                      offset="100%"
                      className="chart-gradient-stop chart-gradient-stop--bottom"
                    />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} />
                <XAxis
                  type="number"
                  dataKey="at"
                  domain={[Date.parse(summary.from), Date.parse(summary.to)]}
                  tickFormatter={timeTick}
                  scale="time"
                  interval="preserveStartEnd"
                  minTickGap={18}
                  tickMargin={8}
                  axisLine={false}
                  tickLine={false}
                  tick={tick}
                />
                <YAxis
                  width={54}
                  domain={[0, yTicks[yTicks.length - 1]]}
                  ticks={yTicks}
                  tickFormatter={(value: number) => latencyValue(value).replace(' ', '\u00a0')}
                  axisLine={false}
                  tickLine={false}
                  tick={tick}
                  tickMargin={6}
                />
                <Tooltip
                  cursor={{ className: 'chart-cursor-line' }}
                  isAnimationActive={!reducedMotion}
                  content={({ active, payload }) => {
                    if (!active || !payload?.length) return null;
                    const row = payload[0].payload as ChartRow;
                    return (
                      <ChartTooltipCard
                        title={formatLocalWithOffset(new Date(row.at))}
                        facts={[{ label: 'Responses', value: row.samples }]}
                        rows={[
                          {
                            key: 'median',
                            swatch: <LineSwatch tone="median" />,
                            label: 'Median',
                            value: row.medianMs === null ? 'No median' : latencyValue(row.medianMs),
                          },
                          {
                            key: 'max',
                            swatch: <LineSwatch tone="max" />,
                            label: 'Max',
                            value: row.maxMs === null ? 'No response' : latencyValue(row.maxMs),
                          },
                        ]}
                      />
                    );
                  }}
                />
                {outageSpans.map((area) => (
                  <ReferenceArea
                    key={`${area.from}-${area.to}`}
                    x1={area.from}
                    x2={area.to}
                    className="chart-band chart-band--outage"
                  />
                ))}
                {wide ? (
                  <BandPills
                    spans={outageSpans}
                    domain={[Date.parse(summary.from), Date.parse(summary.to)]}
                    deadline={{ ms: deadlineMs, yMax: yTicks[yTicks.length - 1] }}
                  />
                ) : null}
                <ReferenceLine
                  y={deadlineMs}
                  ifOverflow="extendDomain"
                  className="chart-deadline"
                  label={<DeadlineLabel />}
                />
                <Area
                  type="linear"
                  dataKey="medianMs"
                  className="chart-area chart-area--median"
                  fill={`url(#${gradientId})`}
                  stroke="none"
                  connectNulls={false}
                  activeDot={false}
                  tooltipType="none"
                  legendType="none"
                  isAnimationActive={!reducedMotion}
                />
                <Line
                  type="linear"
                  dataKey="maxMs"
                  name="Maximum response time"
                  className="chart-line chart-line--max"
                  connectNulls={false}
                  dot={isolatedDot('maxMs')}
                  activeDot={{ r: 3.5, className: 'chart-active-dot chart-active-dot--max' }}
                  isAnimationActive={!reducedMotion}
                />
                <Line
                  type="linear"
                  dataKey="medianMs"
                  name="Median response time"
                  className="chart-line chart-line--median"
                  connectNulls={false}
                  dot={isolatedDot('medianMs')}
                  activeDot={{ r: 4.5, className: 'chart-active-dot chart-active-dot--median' }}
                  isAnimationActive={!reducedMotion}
                />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </>
      )}
    </figure>
  );
}
export default function SummaryCharts({
  summary,
  deadlineMs,
}: {
  summary: MonitorSummary;
  deadlineMs?: number;
}) {
  const reducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)');
  const wide = useMediaQuery('(min-width: 600px)');
  return (
    <div className="summary-charts">
      <HistoryChart summary={summary} reducedMotion={reducedMotion} wide={wide} />
      {summary.kind === 'http' && deadlineMs !== undefined ? (
        <LatencyChart
          summary={summary}
          deadlineMs={deadlineMs}
          reducedMotion={reducedMotion}
          wide={wide}
        />
      ) : null}
    </div>
  );
}
