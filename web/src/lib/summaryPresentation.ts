import type { HeartbeatSummary, HttpSummary, MonitorSummary } from '../api/dailyUse';
import { formatLocalWithOffset } from './time';

export function duration(seconds: number): string {
  if (seconds < 60) return `${seconds} s`;
  if (seconds % 3600 === 0) return `${seconds / 3600} h`;
  return `${Math.round(seconds / 60)} min`;
}
export function coverageSentence(summary: MonitorSummary): string {
  if (summary.kind === 'heartbeat') {
    const c = summary.coverage;
    const ratio =
      c.expected === 0
        ? 'no expected deadlines'
        : `${Math.round((c.recorded / c.expected) * 100)} %`;
    return `${summary.window === '24h' ? 'Last 24 hours' : 'Last 7 days'}: ${c.recorded} of ${c.expected} expected heartbeat deadlines recorded (${ratio}); ${c.notObserved} not observed. Of ${c.recorded - c.maintenance} counted deadlines: ${c.outcomes.onTime} on time, ${c.outcomes.late} late, ${c.outcomes.missed} missed; ${c.outcomes.failureReports} failure reports (included in on time or late). Paused ${duration(c.pausedSeconds)}; maintenance ${c.maintenance} deadlines; ${c.notCounted} not counted.`;
  }
  const c = summary.coverage;
  const counted = c.outcomes.healthy + c.outcomes.failing + c.outcomes.checkerProblem;
  const ratio =
    c.expected === 0 ? 'no expected checks' : `${Math.round((c.recorded / c.expected) * 100)} %`;
  const label = summary.window === '24h' ? 'Last 24 hours' : 'Last 7 days';
  return `${label}: ${c.recorded} of ${c.expected} expected checks recorded (${ratio}); ${c.notObserved} not observed. Of ${counted} counted: ${c.outcomes.healthy} healthy, ${c.outcomes.failing} failing, ${c.outcomes.checkerProblem} checker problem. Paused ${duration(c.pausedSeconds)}; maintenance ${c.maintenance} checks; ${c.notCounted} not counted. Manual checks ${c.manualChecks} (excluded from expected checks).`;
}
export function latencyValue(ms: number): string {
  return ms >= 1000 ? `${Number((ms / 1000).toFixed(1))} s` : `${ms} ms`;
}
export function latencySentence(summary: HttpSummary): string {
  const latency = summary.latency;
  if (latency.samples < 5 || latency.medianMs === null || latency.p95Ms === null) {
    return `Response time (from this machine): too few samples (${latency.samples} responses; ${latency.noResponse} without response; ${latency.checkerProblems} checker problems).`;
  }
  return `Response time (from this machine): median ${latencyValue(latency.medianMs)}, p95 ${latencyValue(latency.p95Ms)}, max ${latency.maxMs === null ? 'unknown' : latencyValue(latency.maxMs)} over ${latency.samples} responses; ${latency.noResponse} without response; ${latency.checkerProblems} checker problems.`;
}
/** `monitorCreatedAt` suppresses the pause-history note when lifecycle history starts at creation. */
export function summaryNotes(summary: MonitorSummary, monitorCreatedAt: string): string[] {
  const notes: string[] = [];
  if (summary.truncated || Date.parse(summary.coveredFrom) > Date.parse(summary.from))
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
  outage: boolean;
  paused: boolean;
};

/** Merge touching outage/pause spans before drawing labels at narrow widths. */
export function shadedSpans(
  summary: MonitorSummary,
  rows: Pick<ChartRow, 'from' | 'to' | 'pausedSeconds'>[],
  includePaused: boolean,
): ShadedSpan[] {
  const spans: ShadedSpan[] = summary.outages.map((outage) => ({
    from: Date.parse(outage.from),
    to: Date.parse(outage.to ?? summary.to),
    outage: true,
    paused: false,
  }));
  if (includePaused) {
    // Paused seconds only locate a bucket, not exact pause boundaries.
    for (const row of rows) {
      if (row.pausedSeconds > 0) {
        spans.push({
          from: Date.parse(row.from),
          to: Date.parse(row.to),
          outage: false,
          paused: true,
        });
      }
    }
  }
  spans.sort((left, right) => left.from - right.from);
  const merged: ShadedSpan[] = [];
  for (const span of spans) {
    const previous = merged[merged.length - 1];
    if (previous && span.from <= previous.to) {
      previous.to = Math.max(previous.to, span.to);
      previous.outage ||= span.outage;
      previous.paused ||= span.paused;
    } else {
      merged.push({ ...span });
    }
  }
  return merged;
}
