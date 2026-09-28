import { useId, useState } from 'react';
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  Label,
  Legend,
  Line,
  ReferenceArea,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { HttpSummary, Span } from '../api/dailyUse';
import {
  chartRows,
  latencyAxisMaximum,
  latencyPlotRows,
  shadedSpans,
} from '../lib/summaryPresentation';
import { formatLocalWithOffset } from '../lib/time';

const series = [
  { key: 'healthy', label: 'Healthy', color: '#217949' },
  { key: 'failing', label: 'Failing', color: '#b43535' },
  { key: 'checkerProblem', label: 'Checker problem', color: '#985c09' },
  { key: 'notObserved', label: 'Not observed', color: '#555b69' },
  { key: 'maintenance', label: 'Maintenance', color: '#6560a2' },
  { key: 'notCounted', label: 'Not counted', color: '#537e94' },
] as const;

function timeTick(value: number) {
  return new Date(value).toLocaleDateString(undefined, {
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
  });
}
function ChartTable({
  summary,
  latency,
  deadlineMs,
}: {
  summary: HttpSummary;
  latency: boolean;
  deadlineMs?: number;
}) {
  return (
    <div className="table-wrapper">
      <table className="observations summary-table">
        <caption>
          {latency ? (
            <>
              Response time by bucket — {summary.window}, {summary.bucketSeconds / 3600} h buckets.
              Denominator: responses within the check deadline ({deadlineMs} ms); no-response and
              checker problems excluded. Empty buckets have no line.{' '}
            </>
          ) : (
            <>
              Status history by bucket — {summary.window}, {summary.bucketSeconds / 3600} h buckets.
              Denominator: expected scheduled checks; paused time excluded.{' '}
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
                {series.map((item) => (
                  <th scope="col" key={item.key}>
                    {item.label}
                  </th>
                ))}
                <th scope="col">Paused seconds</th>
                <th scope="col">Receive outage</th>
              </>
            )}
          </tr>
        </thead>
        <tbody>
          {chartRows(summary).map((row) => (
            <tr key={row.from}>
              <th scope="row">
                {formatLocalWithOffset(new Date(row.from))} –{' '}
                {formatLocalWithOffset(new Date(row.to))}
              </th>
              {latency ? (
                <>
                  <td>{row.samples}</td>
                  <td>{row.medianMs === null ? 'No median' : `${row.medianMs} ms`}</td>
                  <td>{row.maxMs === null ? 'No response' : `${row.maxMs} ms`}</td>
                </>
              ) : (
                <>
                  <td>{row.expected}</td>
                  <td>{row.recorded}</td>
                  {series.map((item) => (
                    <td key={item.key}>{row[item.key]}</td>
                  ))}
                  <td>{row.pausedSeconds}</td>
                  <td>
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
    </div>
  );
}
function HistoryChart({
  summary,
  reducedMotion,
}: {
  summary: HttpSummary;
  reducedMotion: boolean;
}) {
  const [table, setTable] = useState(false);
  const rows = chartRows(summary);
  const captionId = useId();
  return (
    <figure className="summary-figure">
      <figcaption id={captionId}>
        Status history — {summary.window}, {summary.bucketSeconds / 3600} h buckets. Denominator:
        expected scheduled checks; paused time excluded.
        {summary.truncated
          ? ` Partial window from ${formatLocalWithOffset(new Date(summary.coveredFrom))}.`
          : ' Unobserved slots are not treated as healthy or failing.'}
      </figcaption>
      <p className="summary-shading-key">
        Shaded spans: <span className="shade-key--outage">Receive outage</span> — StatusForge was
        not receiving; <span className="shade-key--paused">Paused</span> — monitoring paused.
      </p>
      <button
        className="button"
        type="button"
        aria-pressed={table}
        onClick={() => setTable(!table)}
      >
        {table ? 'Show as chart' : 'Show as table'}
      </button>
      {table ? (
        <ChartTable summary={summary} latency={false} />
      ) : (
        <div className="summary-plot">
          <ResponsiveContainer width="100%" height={290}>
            <ComposedChart
              data={rows}
              accessibilityLayer
              aria-labelledby={captionId}
              margin={{ top: 12, right: 8, bottom: 16, left: 0 }}
            >
              <defs>
                {series.map((item, index) => (
                  <pattern
                    key={item.key}
                    id={`status-pattern-${item.key}`}
                    patternUnits="userSpaceOnUse"
                    width="8"
                    height="8"
                    patternTransform={`rotate(${index * 17 + 30})`}
                  >
                    <rect width="8" height="8" fill={item.color} />
                    <path d="M 0 0 L 0 8" stroke="#fff" strokeWidth="2" />
                  </pattern>
                ))}
              </defs>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis
                type="number"
                dataKey="at"
                domain={[Date.parse(summary.from), Date.parse(summary.to)]}
                tickFormatter={timeTick}
                scale="time"
                interval="preserveStartEnd"
              />
              <YAxis allowDecimals={false} />
              <Tooltip labelFormatter={(value) => formatLocalWithOffset(new Date(Number(value)))} />
              <Legend />
              {shadedSpans(summary, rows, true).map((area) => (
                <ReferenceArea
                  key={`${area.from}-${area.to}`}
                  x1={area.from}
                  x2={area.to}
                  fill={area.outage ? '#e3a4a4' : '#d4d4dc'}
                  fillOpacity={0.32}
                  label={
                    window.innerWidth >= 600
                      ? {
                          value:
                            area.outage && area.paused
                              ? 'Outage + pause'
                              : area.outage
                                ? 'Outage'
                                : 'Paused',
                          position: 'insideTop',
                          fontSize: 10,
                        }
                      : false
                  }
                />
              ))}
              {series.map((item) => (
                <Bar
                  key={item.key}
                  dataKey={item.key}
                  name={item.label}
                  stackId="slots"
                  fill={`url(#status-pattern-${item.key})`}
                  isAnimationActive={!reducedMotion}
                />
              ))}
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      )}
    </figure>
  );
}
function LatencyChart({
  summary,
  deadlineMs,
  reducedMotion,
}: {
  summary: HttpSummary;
  deadlineMs: number;
  reducedMotion: boolean;
}) {
  const [table, setTable] = useState(false);
  const rows = latencyPlotRows(summary);
  const captionId = useId();
  return (
    <figure className="summary-figure">
      <figcaption id={captionId}>
        Response time — {summary.window}, {summary.bucketSeconds / 3600} h buckets. Denominator:
        responses within the check deadline ({deadlineMs} ms); no-response and checker problems
        excluded. Empty buckets have no line.{' '}
        {summary.truncated
          ? `Partial window from ${formatLocalWithOffset(new Date(summary.coveredFrom))}.`
          : 'Measured from this machine, not user experience.'}
      </figcaption>
      <p className="summary-shading-key">
        Shaded spans: <span className="shade-key--outage">Receive outage</span> — StatusForge was
        not receiving.
      </p>
      <button
        className="button"
        type="button"
        aria-pressed={table}
        onClick={() => setTable(!table)}
      >
        {table ? 'Show as chart' : 'Show as table'}
      </button>
      {table ? (
        <ChartTable summary={summary} latency deadlineMs={deadlineMs} />
      ) : (
        <div className="summary-plot">
          <ResponsiveContainer width="100%" height={290}>
            <ComposedChart
              data={rows}
              accessibilityLayer
              aria-labelledby={captionId}
              margin={{ top: 12, right: 16, bottom: 16, left: 0 }}
            >
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis
                type="number"
                dataKey="at"
                domain={[Date.parse(summary.from), Date.parse(summary.to)]}
                tickFormatter={timeTick}
                scale="time"
                interval="preserveStartEnd"
              />
              <YAxis
                domain={[0, latencyAxisMaximum(summary, deadlineMs)]}
                tickFormatter={(value: number) => `${value}\u00a0ms`}
              />
              <Tooltip labelFormatter={(value) => formatLocalWithOffset(new Date(Number(value)))} />
              <Legend />
              {shadedSpans(summary, chartRows(summary), false).map((area) => (
                <ReferenceArea
                  key={`${area.from}-${area.to}`}
                  x1={area.from}
                  x2={area.to}
                  fill="#e3a4a4"
                  fillOpacity={0.32}
                  label={
                    window.innerWidth >= 600
                      ? {
                          value: 'Outage',
                          position: 'insideTop',
                          fontSize: 10,
                        }
                      : false
                  }
                />
              ))}
              <ReferenceLine
                y={deadlineMs}
                ifOverflow="extendDomain"
                stroke="#985c09"
                strokeDasharray="5 3"
              >
                <Label value="Check deadline" position="insideTopRight" />
              </ReferenceLine>
              <Line
                type="linear"
                dataKey="medianMs"
                name="Median response time"
                stroke="#276d9c"
                strokeWidth={2}
                connectNulls={false}
                isAnimationActive={!reducedMotion}
              />
              <Line
                type="linear"
                dataKey="maxMs"
                name="Maximum response time"
                stroke="#a74268"
                strokeWidth={2}
                connectNulls={false}
                isAnimationActive={!reducedMotion}
              />
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      )}
    </figure>
  );
}
export default function SummaryCharts({
  summary,
  deadlineMs,
}: {
  summary: HttpSummary;
  deadlineMs: number;
}) {
  const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
  return (
    <div className="summary-charts">
      <HistoryChart summary={summary} reducedMotion={reducedMotion} />
      <LatencyChart summary={summary} deadlineMs={deadlineMs} reducedMotion={reducedMotion} />
    </div>
  );
}
