# Heartbeats and deployments

Signals StatusForge does not produce itself: jobs report that they ran (heartbeats), and deploy pipelines
report that something changed (deployment markers). The design goal is to treat "the job did not report"
and "StatusForge could not receive" as two different facts, and to let markers add context without ever
changing health.

Implementation: `backend/internal/heartbeat/` (validation, deadline arithmetic, outage containment, tokens),
`backend/internal/store/heartbeat.go`, `applications.go`, `deployments.go`, `backend/internal/scheduler/`
(deadline step, liveness writer), `backend/internal/httpapi/heartbeat.go`, `applications.go`. Rationale:
[ADR 0005](../decisions/ADR-0005-heartbeats-deployments.md).

## A heartbeat is a monitor kind

`kind: heartbeat` monitors have no `check` and no `WORK#` items. They carry
`heartbeat {intervalSeconds, graceSeconds, token {hash, hint, createdAt}}` and
`expectation {reference, dueAt, lastFinishedAt}`. Both a received report and a missed deadline are stored as
**observations**, so the whole incident machinery — evaluation in the result transaction, incidents, outbox,
reminders, maintenance windows, pause and archive — applies unchanged.

| Observation `kind` | `at` | `initiatedBy` | `outcome` / `reason` |
| --- | --- | --- | --- |
| `heartbeat_report` | receipt time | `job` | `healthy` / `ok`, or `failing` / `reported_failure` |
| `heartbeat_missed` | `dueAt + grace` | `statusforge` | `failing` / `missing` |

Allowed intervals: 5 min … 7 days (plus 10/15/30 s when the dev minimum is unlocked); grace 1 min … 6 h,
≤ interval. The default incident policy for heartbeats is `{openAfter: 1, recoverAfter: 1}`.

## Ingest

```
POST /ingest/heartbeats/{id}
Authorization: Bearer sfh_…
Content-Type: application/json
{"status":"success","runId":"2026-10-01T06:00:00Z","finishedAt":"…","durationMs":1234,"exitCode":0,"message":"…"}
```

Order of checks: body size (4 KiB → `413`), authentication (`401` for unknown ID, non-heartbeat monitor,
missing, revoked, or wrong token — one body, no hint which), rate limit (`429` + `Retry-After`), lifecycle
(`410 archived`), validation (`400` field errors), then the write (`503` on store failure). An empty body is
a plain success report.

Responses: `202 {"accepted":true,"duplicate":false,"counted":…,"notCountedReason":…,"late":…,"nextDueAt":…}`,
or `200 {"accepted":true,"duplicate":true}` for a repeated `runId` within 7 days (nothing written).

The recording transaction puts the observation, the `RUN#<runId>` guard (`attribute_not_exists`), and
updates the monitor conditioned on `lifecycle = active`, `kind = heartbeat`, the token hash that
authenticated, and `evaluation.revision`. A report whose `finishedAt` is older than the newest counted one
is stored `counted: false` (`older_than_current`). A paused monitor's report is stored not-counted
(`paused`). Receipt time decides `late` and the next deadline; `finishedAt` is used only for ordering.

### Tokens

`sfh_` + 32 random bytes base32 (heartbeat), `sfd_` + the same (application). Only the SHA-256 digest and the
last 4 characters are stored; comparison is constant-time. The plaintext appears once, in the issuing
response. Issue/rotate is conditional on the previous hash; revoke removes it. Request logs omit
`Authorization`. Rate limiting is a per-ID token bucket (1/s, burst 5) applied **after** authentication so a
flood of bad tokens cannot drain a real job's allowance; failed authentication is limited per remote address.

## Deadlines and receive liveness

The problem: if StatusForge was down (or its database was) when a job tried to report, a missed deadline is
not evidence about the job. The scheduler therefore keeps a durable record of when it could receive.

```mermaid
sequenceDiagram
  participant T as Tick (every 2 s)
  participant L as SYSTEM/LIVENESS
  participant M as Monitor item
  T->>L: every 10 s: aliveAt = now; if previous aliveAt > 30 s old, append outage (prev, now)
  T->>M: for each active heartbeat with dueAt + grace ≤ now (only if aliveAt ≥ dueAt + grace)
  alt an outage overlaps (reference, dueAt + grace]
    T->>M: GAP#dueAt reason=not_observed (missedCount = collapsed deadlines), clear runs,<br/>reference = outage.to, dueAt = outage.to + interval
  else
    T->>M: OBS heartbeat_missed (newest collapsed deadline) → incident evaluation;<br/>older collapsed deadlines → one GAP reason=overdue
  end
  Note over M: condition expectation.dueAt = :due ∧ active — idempotent across ticks and processes
```

- The deadline step runs only when `aliveAt ≥ dueAt + grace`, so an outage is always known before a deadline
  inside it is judged.
- After an outage the job gets a **full window** from the outage end (it may have tried to report while
  StatusForge was down). Consequence, stated in the UI: a job that really stopped during an outage is reported
  one full interval after StatusForge is receiving again.
- Outages shorter than 30 s (3 × the liveness interval) are not detected.
- Process restarts and database outages both appear as receive outages, because reports could not be stored
  during either (ingest returns `503` while the store is unavailable).

`GET /api/system/liveness` returns `{aliveAt, outages[]}` (newest first, ≤ 50); summaries and the Overview
show outage periods as shaded spans, and a heartbeat's presented status includes `late` (after `dueAt`, before
`dueAt + grace`) and `stale` (deadline passed but not yet judged: `missingAt + 3 × liveness + 5 s`).

## Applications and deployment markers

An application groups monitors (membership lives on the monitor item, at most one application per monitor)
and owns a deployment token.

```
POST /ingest/applications/{id}/deployments
Authorization: Bearer sfd_…
{"version":"1.4.2","description":"…","link":"https://…","deployedAt":"…","deploymentId":"build-812"}
```

Same order of checks, limits, and codes as heartbeat ingest. A marker and its `DEPID#<deploymentId>` guard
share a transaction; cancellation on the guard alone means duplicate (`200 duplicate`). Markers can also be
created manually through the API (`source: manual`). They are never updated or deleted, and they are **not
read** by status evaluation, incident evaluation, or notifications.

An incident records the monitor's `applicationId` when it opens. Incident detail lists **nearby deployments**:
markers of that application with `reportedAt` in `[openedAt − 2 h, resolvedAt | now]`, oldest first, at most
20, each with `offsetSeconds` from `openedAt`. Recording the application at open keeps markers visible on
past incidents after the application is archived or the monitor moves. `link` is stored and rendered with
`rel="noopener noreferrer"`, never fetched.

## Sample job fixture

`cmd/sample-job` is a stdlib-only loopback job that reports every interval with a fresh `runId`. Its control
API (`PUT /control/mode` with `normal | skip | fail | late | stop`, `POST /control/run`, `POST /control/replay`
for a duplicate `runId`, `POST /control/deploy` to post a marker) makes every heartbeat scenario reproducible
from the [runbook](../operations/local-runbook.md).
