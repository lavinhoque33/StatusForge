# ADR 0006: Coverage and latency summaries, lifecycle history, overview, history paging, charts

- Status: accepted (2026-09-28)
- Extends: [ADR 0005](ADR-0005-heartbeats-deployments.md), [ADR 0004](ADR-0004-incidents-notifications.md),
  [ADR 0003](ADR-0003-scheduling-persistence.md), [ADR 0002](ADR-0002-persistence.md) (same table and key
  style; status evaluated on read)

## Context

This record makes the evidence already collected understandable: what needs attention now, how much of a
period was actually checked, what the outcomes and response times were, and what the limits of each figure
are. Every reliability figure must state its window, denominator, coverage, and treatment of pauses and gaps;
a fraction of successful samples is never presented as time-based availability. Out of scope: dashboard
builders, query languages, speculative analytics, and fabricated continuity during local downtime.

Choices made before design: slot-based coverage; summaries computed on read with rollups deferred;
percentile latency plus a latency chart and a status history chart drawn with a chart library; server-side
history filters with cursor paging; per-monitor fan-out for cross-monitor reads.

Facts this design builds on:

- Every scheduled HTTP slot leaves exactly one record: an observation with `dueAt` (counted or not) or a share of a
  gap (`missedCount` over `fromDueAt..toDueAt`, reasons `not_scheduled`, `overdue`, `lease_expired`). Manual checks
  have `dueAt = null`.
- Every heartbeat deadline leaves a counted report, a `heartbeat_missed` observation, or a share of a gap
  (`not_observed`, `overdue`).
- Pausing creates no slots and no gaps; before this record the monitor stored only the current `pausedAt`.
- Observations carry `durationMs`, `outcome`, `reason`, `observedStatus`, `maintenanceWindowId`, and a declared
  `expiresAt` (90 days; ADR 0002 D8). Receive outages are kept in `SYSTEM`/`LIVENESS` (at most 50).

## Decisions

### D1. Summaries are computed on read, bounded

`GET /api/monitors/{id}/summary?window=24h|7d` reads the monitor's observations (`OBS#` range for the window,
with a `ProjectionExpression` limited to the fields the summary uses), gaps (`GAP#` range, widened by one window
so a gap starting before the window but reaching into it is found), lifecycle events (D3), maintenance windows,
and receive outages, and computes everything in `internal/summary` (pure, stdlib only).

Bounds: at most **20 000** observations and **5 000** gaps per request, read newest first. When a bound is hit the
response has `truncated: true` and `coveredFrom` = the oldest included instant; every figure then describes
`[coveredFrom, to]` and says so. No cache and no stored rollup.

Rejected for now: hourly rollup items updated inside the result, deadline, and gap transactions. They would scale
to 30–90 day windows and survive retention, but they add write-path changes and a backfill, and their shape
depends on the retention decision; 24 h and 7 d windows stay within the 90-day detail.

Cost at the planned local profile: 7 days at a 5-minute interval is 2 016 observations per monitor; at the
60-second production minimum it is 10 080. The web refreshes summaries at most every 60 s (not with the 15 s
poll), only while visible.

### D2. Coverage is slot-based

For a window `[from, to]` and an HTTP monitor:

- A **slot** is a scheduled due instant. Each observation with `dueAt` in the window is one **recorded** slot;
  each gap contributes the slots of its range that fall in the window. A gap's `missedCount` slots are placed
  evenly from `fromDueAt` to `toDueAt` (inclusive; one slot at `fromDueAt` when `missedCount = 1`), which is exact
  for gaps written by the scheduler because they list consecutive slots of one interval.
- **Expected** = recorded + not observed. Manual checks are listed separately and are never in the denominator.
- Recorded slots with `maintenanceWindowId` are reported as **maintenance** and excluded from the outcome counts.
  Recorded slots with `counted = false` are reported as **not counted** with their reasons and excluded from the
  outcome counts.
- **Outcome counts** (healthy, failing, checker problem) cover the remaining recorded slots. The one ratio shown is
  "healthy results among counted checks" with its numerator and denominator; the word "uptime" or "availability"
  is never used.
- **Coverage ratio** = recorded ÷ expected, shown with both numbers.
- **Paused time** (D3) is stated in seconds and not converted into slots.
- **Not yet accounted for:** when the monitor is active and its presented state is `stale`, the time after the
  newest accounted slot is reported as `pendingSince` rather than guessed as missed or healthy; the gap that the
  scheduler writes later accounts for it.

Heartbeats use deadlines as slots: counted reports (on time or late), `heartbeat_missed` observations, and gap
shares; reports with `status: failure` are counted among recorded deadlines and reported as failure reports;
not-counted reports (paused, older than current, archived) are listed separately.

Buckets: 24 h → one-hour buckets; 7 d → six-hour buckets, aligned to multiples of the bucket size in UTC (00, 06,
12, 18 h for six-hour buckets). Because the window ends at the evaluation instant, the first and last bucket are
usually partial and carry their clipped `from`/`to`, so a window normally has 25 (24 h) or 29 (7 d) buckets. Each
bucket repeats the counts for its slots, its paused seconds, and its latency figures.

### D3. Lifecycle history

New item written in the same transaction as each lifecycle change (create, pause, resume, archive):

| Entity | `PK` | `SK` |
| --- | --- | --- |
| Lifecycle event | `MON#<monitorId>` | `LIFE#<at>#<action>` |

```
LifecycleEvent  entityType "lifecycle", monitorId, action ("created" | "paused" | "resumed" | "archived"), at
```

Paused periods are the spans from `paused` to the next `resumed` or `archived` (or `to`). Monitors created before
this record have no events: a monitor currently paused with `pausedAt` is treated as paused since `pausedAt`;
earlier pause periods are unknown, and the summary returns `lifecycleHistoryFrom` (the earliest event, or `null`)
so the web can state that pause periods before that instant are not recorded. No backfill.

### D4. Latency

Latency samples are HTTP observations in the window with `reason` `ok` or `wrong_status` (a response arrived within
the deadline), counted or not, scheduled or manual, **excluding observations labelled with a maintenance window**
(maintenance evidence is reported separately everywhere; a deliberately slow or restarted target should not skew
the figures). Figures: sample count, median, p95, and max `durationMs`, nearest-rank (`p = ⌈q·n⌉`-th smallest).
Observations with `timeout`, `connection_refused`, or `connection_error` are counted as **no response** and checker
problems as **checker problems**; neither enters the percentiles. With fewer than 5 samples the percentiles are
`null` ("too few samples"), and the count is still shown. The figures describe round-trip time from this machine
through the loopback fixture, not user experience.

### D5. Overview is composed on the server by bounded fan-out

`GET /api/overview` reads the monitor list (one partition), each active monitor's open incident pointer and
presented status (already on the monitor read path), recent resolved incidents per monitor (`INC#` query, limited),
failed notifications (the attention read), applications, and receive outages, and returns sections in a fixed
order. It performs no summary computation. Per-section limits are stated in the response. This is the same
bounded fan-out as ADR 0004 D1 and ADR 0005 D9; a global time-ordered index stays rejected until a demonstrated
need.

### D6. History paging and filters

`GET /api/monitors/{id}/observations` and `/gaps` gain `before` (opaque cursor: base64url of the monitor ID and the
last returned sort key, so a cursor from another monitor is rejected; encoding the sort key alone cannot prove
ownership) and, for observations, filters `outcome`, `counted`, `maintenance`, `from`, `to` (compared with the
observation's start time, the sort-key order). Filters run as a DynamoDB `FilterExpression` on a
`KeyConditionExpression` range; each request examines at most **2 000** items (`Limit` per page, repeated while
under the bound). The response returns `nextCursor` (or `null` when the range is exhausted) and `searchedThrough`
(the oldest examined instant), so the web can say "searched back to …" when fewer than `limit` items matched.
Cursors are keys, not offsets, so new observations arriving at the head never shift older pages (no duplicates
or skips).

### D7. Chart library: Recharts

Choice: **Recharts 3.10.1** (MIT; SVG output; React 19 in its peer range; `accessibilityLayer` on by default in
3.x, giving a single tab stop with arrow-key navigation), pinned exactly, with `react-is` pinned to the installed
React version as its peer.

Rules:

- Charts are loaded with `React.lazy` in their own chunk, so the Overview, lists, and forms do not pay for them.
- Every chart has a caption stating the window, bucket size, denominator, and limits, and a "Show as table" toggle
  rendering the same bucket data as a table; the table is the reference for tests and screen readers.
- Status history: stacked bars per bucket for healthy, failing, checker problem, not observed, maintenance, not
  counted; each series has a pattern fill as well as a colour and a text legend; paused spans and receive outages
  are shaded reference areas with text labels.
- Latency: median and max lines per bucket; buckets without samples are `null` and lines are **not connected**
  across them (no fabricated continuity); the check deadline is a labelled reference line.
- No animation when `prefers-reduced-motion` is set.

Rejected: Chart.js (canvas output: no DOM for assistive technology or tests), visx (low-level primitives: most of
the drawing would be hand-written), Nivo (heavier, more dependencies for the same charts).

### D8. Package boundaries

- `internal/summary` (stdlib only): slot attribution, gap spreading, bucket alignment, paused spans, outcome and
  coverage counts, nearest-rank percentiles.
- `internal/store`: bounded window reads, lifecycle events in the lifecycle transaction, overview reads, cursor
  paging.
- `internal/httpapi`: `/api/monitors/{id}/summary`, `/api/overview`, paging parameters.
- `web`: Overview page, summary panel, lazy chart components, table equivalents.

## Alternatives considered

- **Time-based coverage** (each observation covers time until the next or until stale): readable, but needs its
  own model and invites reading it as availability.
- **Rollups now:** see D1; deferred with retention.
- **Client-side overview from existing endpoints:** one request per monitor from the browser every 15 s; the
  server composition keeps one request and one place for the section rules.
- **Hand-drawn status strip and no latency chart:** declined in favour of a chart library.

## Consequences

- Summary figures are exact for the records that exist, bounded, and labelled when partial; windows longer than
  7 days would need rollups.
- Lifecycle changes write one more item per transaction; pause history exists only from this record onwards and
  the UI says so.
- The web gains its first charting dependency, kept out of the initial chunk.
- Retention for observations, gaps, lifecycle events, and any rollups is decided together in
  [ADR 0007](ADR-0007-retention-and-release.md).
