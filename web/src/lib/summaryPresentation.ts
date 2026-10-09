import type { HeartbeatSummary, HttpSummary, MonitorSummary } from '../api/dailyUse';
import { formatLocalWithOffset } from './time';

export function duration(seconds: number): string {
  if (seconds < 60) return `${seconds} s`;
  if (seconds % 3600 === 0) return `${seconds / 3600} h`;
  return `${Math.round(seconds / 60)} min`;
}
/** Recorded share of expected slots, exactly as the coverage sentence prints it. */
export function coverageRatio(summary: MonitorSummary): string {
  const c = summary.coverage;
  if (c.expected === 0)
    return summary.kind === 'heartbeat' ? 'no expected deadlines' : 'no expected checks';
  return `${Math.round((c.recorded / c.expected) * 100)} %`;
}
/** Outcomes the coverage sentence counts: recorded minus maintenance for heartbeats. */
export function countedOutcomes(summary: MonitorSummary): number {
  if (summary.kind === 'heartbeat') return summary.coverage.recorded - summary.coverage.maintenance;
  const o = summary.coverage.outcomes;
  return o.healthy + o.failing + o.checkerProblem;
}
export function coverageSentence(summary: MonitorSummary): string {
  const ratio = coverageRatio(summary);
  const counted = countedOutcomes(summary);
  if (summary.kind === 'heartbeat') {
    const c = summary.coverage;
    return `${summary.window === '24h' ? 'Last 24 hours' : 'Last 7 days'}: ${c.recorded} of ${c.expected} expected heartbeat deadlines recorded (${ratio}); ${c.notObserved} not observed. Of ${counted} counted deadlines: ${c.outcomes.onTime} on time, ${c.outcomes.late} late, ${c.outcomes.missed} missed; ${c.outcomes.failureReports} failure reports (included in on time or late). Paused ${duration(c.pausedSeconds)}; maintenance ${c.maintenance} deadlines; ${c.notCounted} not counted.`;
  }
  const c = summary.coverage;
  const label = summary.window === '24h' ? 'Last 24 hours' : 'Last 7 days';
  return `${label}: ${c.recorded} of ${c.expected} expected checks recorded (${ratio}); ${c.notObserved} not observed. Of ${counted} counted: ${c.outcomes.healthy} healthy, ${c.outcomes.failing} failing, ${c.outcomes.checkerProblem} checker problem. Paused ${duration(c.pausedSeconds)}; maintenance ${c.maintenance} checks; ${c.notCounted} not counted. Manual checks ${c.manualChecks} (excluded from expected checks).`;
}
/**
 * Round latency axis ticks from 0 up to at least `maximum`, stepping by 1, 2,
 * 2.5 or 5 × 10^n so `latencyValue` labels read cleanly (`1 s`, `250 ms`).
 */
export function latencyTicks(maximum: number, intervals = 4): number[] {
  if (maximum <= 0) return [0];
  const raw = maximum / intervals;
  const magnitude = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((factor) => factor * magnitude).find((s) => s >= raw)!;
  return Array.from({ length: Math.ceil(maximum / step) + 1 }, (_, index) =>
    Math.round(index * step),
  );
}
export function latencyValue(ms: number): string {
  return ms >= 1000 ? `${Number((ms / 1000).toFixed(1))} s` : `${ms} ms`;
}
export type LatencyFigures = { medianMs: number; p95Ms: number; maxMs: number | null };
/** The one sample gate for window latency: `null` means too few samples to state figures. */
export function latencyFigures(latency: HttpSummary['latency']): LatencyFigures | null {
  if (latency.samples < 5 || latency.medianMs === null || latency.p95Ms === null) return null;
  return { medianMs: latency.medianMs, p95Ms: latency.p95Ms, maxMs: latency.maxMs };
}
export function latencySentence(summary: HttpSummary): string {
  const latency = summary.latency;
  const figures = latencyFigures(latency);
  if (figures === null) {
    return `Response time (from this machine): too few samples (${latency.samples} responses; ${latency.noResponse} without response; ${latency.checkerProblems} checker problems).`;
  }
  return `Response time (from this machine): median ${latencyValue(figures.medianMs)}, p95 ${latencyValue(figures.p95Ms)}, max ${figures.maxMs === null ? 'unknown' : latencyValue(figures.maxMs)} over ${latency.samples} responses; ${latency.noResponse} without response; ${latency.checkerProblems} checker problems.`;
}
/** The window starts later than requested (truncated or newer coverage). */
export function isPartialWindow(summary: MonitorSummary): boolean {
  return summary.truncated || Date.parse(summary.coveredFrom) > Date.parse(summary.from);
}
/** `monitorCreatedAt` suppresses the pause-history note when lifecycle history starts at creation. */
export function summaryNotes(summary: MonitorSummary, monitorCreatedAt: string): string[] {
  const notes: string[] = [];
  if (isPartialWindow(summary))
    notes.push(
      `Partial window — covers from ${formatLocalWithOffset(new Date(summary.coveredFrom))}.`,
    );
  if (summary.pendingSince !== null)
    notes.push(
      `Not yet accounted for since ${formatLocalWithOffset(new Date(summary.pendingSince))}.`,
    );
  if (
    summary.lifecycleHistoryFrom === null ||
    (Date.parse(summary.lifecycleHistoryFrom) > Date.parse(summary.from) &&
      Date.parse(summary.lifecycleHistoryFrom) > Date.parse(monitorCreatedAt))
  )
    notes.push(
      `Pause periods before ${summary.lifecycleHistoryFrom === null ? 'lifecycle history began' : formatLocalWithOffset(new Date(summary.lifecycleHistoryFrom))} are not recorded.`,
    );
  return notes;
}
export type ChartRow = {
  from: string;
  to: string;
  at: number;
  expected: number;
  recorded: number;
  healthy: number;
  failing: number;
  checkerProblem: number;
  notObserved: number;
  maintenance: number;
  notCounted: number;
  pausedSeconds: number;
  samples: number;
  medianMs: number | null;
  maxMs: number | null;
};
export function chartRows(summary: HttpSummary): ChartRow[] {
  return summary.buckets.map((bucket) => ({
    from: bucket.from,
    to: bucket.to,
    at: (Date.parse(bucket.from) + Date.parse(bucket.to)) / 2,
    expected: bucket.expected,
    recorded: bucket.recorded,
    healthy: bucket.healthy,
    failing: bucket.failing,
    checkerProblem: bucket.checkerProblem,
    notObserved: bucket.notObserved,
    maintenance: bucket.maintenance,
    notCounted: bucket.notCounted,
    pausedSeconds: bucket.pausedSeconds,
    samples: bucket.latency.samples,
    medianMs: bucket.latency.medianMs,
    maxMs: bucket.latency.maxMs,
  }));
}

export type HeartbeatChartRow = {
  from: string;
  to: string;
  at: number;
  expected: number;
  recorded: number;
  onTime: number;
  late: number;
  missed: number;
  failureReports: number;
  notObserved: number;
  maintenance: number;
  notCounted: number;
  pausedSeconds: number;
};
export function heartbeatChartRows(summary: HeartbeatSummary): HeartbeatChartRow[] {
  return summary.buckets.map((bucket) => ({
    from: bucket.from,
    to: bucket.to,
    at: (Date.parse(bucket.from) + Date.parse(bucket.to)) / 2,
    expected: bucket.expected,
    recorded: bucket.recorded,
    onTime: bucket.onTime,
    late: bucket.late,
    missed: bucket.missed,
    failureReports: bucket.failureReports,
    notObserved: bucket.notObserved,
    maintenance: bucket.maintenance,
    notCounted: bucket.notCounted,
    pausedSeconds: bucket.pausedSeconds,
  }));
}

/** Recharts passes the categorical bucket midpoint as the tooltip label. */
export function statusBucketLabel(
  rows: Pick<ChartRow, 'at' | 'from' | 'to'>[],
  value: unknown,
): string {
  const bucket = rows.find((row) => row.at === Number(value));
  return bucket
    ? `${formatLocalWithOffset(new Date(bucket.from))} – ${formatLocalWithOffset(new Date(bucket.to))}`
    : 'Unknown bucket';
}

/** Break plotted lines across receiver outages without changing bucket totals. */
export function latencyPlotRows(summary: HttpSummary): ChartRow[] {
  const rows = chartRows(summary);
  if (rows.length < 2) return rows;
  const first = rows[0].at;
  const last = rows[rows.length - 1].at;
  const breaks = summary.outages.flatMap((outage) => {
    const start = Date.parse(outage.from);
    const end = Date.parse(outage.to ?? summary.to);
    const at = (start + end) / 2;
    if (at <= first || at >= last) return [];
    return [{ ...rows[0], at, samples: 0, medianMs: null, maxMs: null }];
  });
  return [...rows, ...breaks].sort((left, right) => left.at - right.at);
}

/** Include the deadline even when every observed response is much faster. */
export function latencyAxisMaximum(summary: HttpSummary, deadlineMs: number): number {
  const maximum = summary.buckets.reduce(
    (highest, bucket) => Math.max(highest, bucket.latency.maxMs ?? 0),
    deadlineMs,
  );
  return Math.ceil(maximum * 1.1);
}

export type ShadedSpan = {
  from: number;
  to: number;
  kind: 'outage' | 'paused';
};

/**
 * Receive-outage and paused spans, sorted by start. Touching or overlapping
 * spans merge only within the same kind, so a pause is never drawn as an
 * outage; spans of different kinds may touch or overlap.
 */
export function shadedSpans(
  summary: MonitorSummary,
  rows: Pick<ChartRow, 'from' | 'to' | 'pausedSeconds'>[],
  includePaused: boolean,
): ShadedSpan[] {
  const spans: ShadedSpan[] = summary.outages.map((outage) => ({
    from: Date.parse(outage.from),
    to: Date.parse(outage.to ?? summary.to),
    kind: 'outage',
  }));
  if (includePaused) {
    // Paused seconds only locate a bucket, not exact pause boundaries.
    for (const row of rows) {
      if (row.pausedSeconds > 0) {
        spans.push({ from: Date.parse(row.from), to: Date.parse(row.to), kind: 'paused' });
      }
    }
  }
  spans.sort((left, right) => left.from - right.from);
  const merged: ShadedSpan[] = [];
  const open: Partial<Record<ShadedSpan['kind'], ShadedSpan>> = {};
  for (const span of spans) {
    const previous = open[span.kind];
    if (previous && span.from <= previous.to) {
      previous.to = Math.max(previous.to, span.to);
    } else {
      const next = { ...span };
      open[span.kind] = next;
      merged.push(next);
    }
  }
  return merged;
}
