# Summaries and overview

How collected evidence is turned into figures that state their own denominator, coverage, and limits — and
never into an "uptime percentage".

Implementation: `backend/internal/summary/` (pure, stdlib only), `backend/internal/store/summary.go`,
`overview.go`, `history.go`, `backend/internal/httpapi/summary.go`, `history.go`. Rationale:
[ADR 0006](../decisions/ADR-0006-summaries-overview.md).

## Why slot-based coverage

A monitoring tool that was itself down for an hour has no evidence for that hour. Time-based availability
("the target was up 99.3 % of the week") silently fills that hour with an assumption. StatusForge instead
counts **slots** — the due instants the scheduler owed — and reports how many were recorded, how many were
not observed, and what the recorded ones showed. The only ratio presented is "healthy results among counted
checks", with its numerator and denominator beside it; the words *uptime* and *availability* do not appear
in the UI.

Every scheduled slot leaves exactly one record ([scheduling](scheduling.md)): an observation with `dueAt`
(counted or not) or a share of a gap. That is what makes the arithmetic exact rather than estimated.

## `GET /api/monitors/{id}/summary?window=24h|7d`

Reads, for the window: observations (`OBS#` range, projected to the fields used), gaps (`GAP#` range widened by
one window so a gap starting earlier but reaching in is found), lifecycle events, maintenance windows, and
receive outages. Computes everything on read. Bounds: 20 000 observations and 5 000 gaps, newest first;
when hit, `truncated: true` and `coveredFrom` mark the oldest included instant and every figure describes
`[coveredFrom, to]`.

```json
{
  "monitorId": "…", "kind": "http", "window": "24h",
  "from": "…", "to": "…", "evaluatedAt": "…",
  "truncated": false, "coveredFrom": "…", "bucketSeconds": 3600,
  "lifecycleHistoryFrom": "…", "pendingSince": null,
  "coverage": {
    "expected": 288, "recorded": 280, "notObserved": 8,
    "maintenance": 6, "notCounted": 2, "notCountedReasons": {"config_changed": 2},
    "outcomes": {"healthy": 270, "failing": 2, "checkerProblem": 0},
    "manualChecks": 1, "pausedSeconds": 1800
  },
  "latency": {"samples": 272, "medianMs": 12, "p95Ms": 40, "maxMs": 1510, "noResponse": 0, "checkerProblems": 0},
  "outages": [{"from": "…", "to": "…"}],
  "maintenanceWindows": [{"id": "…", "from": "…", "to": "…"}],
  "buckets": [{"from": "…", "to": "…", "expected": 12, "recorded": 12, "notObserved": 0, "maintenance": 0,
               "notCounted": 0, "healthy": 12, "failing": 0, "checkerProblem": 0, "pausedSeconds": 0,
               "latency": {"samples": 12, "medianMs": 11, "p95Ms": null, "maxMs": 14}}]
}
```

### Attribution rules

- **Recorded** = observations with `dueAt` in the window. **Not observed** = gap slots in the window; a gap's
  `missedCount` slots are placed evenly from `fromDueAt` to `toDueAt`, which is exact because gaps list
  consecutive slots of one interval. **Expected** = recorded + not observed.
- Manual checks are listed separately (`manualChecks`) and never enter the denominator.
- Maintenance-labelled slots are reported as `maintenance`; `counted: false` slots as `notCounted` with
  reasons; both are excluded from `outcomes`. `outcomes` sums to `recorded − maintenance − notCounted`.
- Paused time is reported in seconds (from `LIFE#` events), not converted into slots. Monitors created before
  lifecycle events existed report `lifecycleHistoryFrom` so the UI can say earlier pauses are not recorded.
- `pendingSince`: when the monitor is active and presented `stale`, the time after the newest accounted slot
  is reported as pending rather than guessed; the gap the scheduler writes later accounts for it.
- **Latency** samples are HTTP observations with `reason` `ok` or `wrong_status` (a response arrived within the
  deadline), excluding maintenance-labelled ones. Nearest-rank median, p95, max; `null` below 5 samples.
  Timeouts and connection failures are `noResponse`, never in the percentiles. The figures describe
  round-trip time from this machine through the loopback fixture, not user experience.
- **Buckets**: 24 h → 1 h, 7 d → 6 h, aligned to UTC multiples; first and last are clipped, so a window has 25
  or 29 buckets covering `[from, to]` without holes.

Heartbeats use deadlines as slots: `outcomes` is `{onTime, late, missed, failureReports}`; not-counted
reports have no deadline and are listed as separate evidence; `latency` is `null`.

### Charts

Recharts, lazy-loaded in its own chunk. Status history: stacked bars per bucket (healthy, failing, checker
problem, not observed, maintenance, not counted) with pattern fills and a text legend; paused spans and
receive outages as labelled reference areas. Latency: median and max lines; buckets without samples are
`null` and lines are **not connected** across them — no fabricated continuity. Every chart has a caption
stating window, bucket size, denominator, and limits, and a "Show as table" toggle rendering the same bucket
data; the table is the reference for tests and screen readers. No animation under `prefers-reduced-motion`.

## `GET /api/overview`

Composed on the server by bounded fan-out: the monitor list (one partition), each active monitor's presented
status and open-incident pointer, recent resolved incidents per monitor (limited), failed notifications,
applications, receive outages, and the in-process scheduler coverage state. No summary computation.

```json
{
  "evaluatedAt": "…",
  "receiveOutages": [{"from": "…", "to": "…"}],
  "scheduler": {"state": "ok", "windowMinutes": 5, "dueChecks": 120, "missedChecks": 0, "workers": 4},
  "openIncidents": [{"monitor": MonitorRef, "incident": Incident}],
  "failingWithoutIncident": [{"monitor": MonitorRef, "status": Status}],
  "coverageProblems": [{"monitor": MonitorRef, "status": Status}],
  "notifications": {"count": 0, "items": [AttentionNotification]},
  "recentRecoveries": [{"monitor": MonitorRef, "incident": Incident}],
  "counts": {"active": 0, "paused": 0, "archived": 0,
             "byState": {"healthy": 0, "late": 0, "failing": 0, "checker_problem": 0, "stale": 0, "unknown": 0}},
  "limits": {"openIncidents": 50, "recentRecoveries": 20, "notifications": 10, "recoveryWindowHours": 24}
}
```

Section order is fixed and mirrors what needs attention first: receive outages, scheduler behind, open
incidents, failing without an incident yet, coverage problems (`stale`, `checker_problem`, `unknown`, `late`
— active monitors only), undelivered notifications, recent recoveries. Limits are stated in the response so
the UI can say "showing 50 of N". A global time-ordered index stays rejected until a demonstrated need.

## History paging and filters

`GET /api/monitors/{id}/observations` and `/gaps` accept `limit`, `before` (opaque cursor), and for
observations `outcome`, `counted`, `maintenance`, `from`, `to`. The cursor is base64url of the monitor ID plus
the last sort key, so a cursor from another monitor is rejected; cursors are keys, not offsets, so new rows
arriving at the head never shift older pages. Filters run as a DynamoDB `FilterExpression` on the key range
and each request examines at most 2 000 items; the response returns `nextCursor` and `searchedThrough` so the
UI can say "searched back to …" when fewer than `limit` items matched.
