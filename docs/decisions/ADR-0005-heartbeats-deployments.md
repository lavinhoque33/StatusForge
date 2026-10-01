# ADR 0005: Heartbeat monitors, receipt credentials, liveness, applications, deployments

- Status: accepted (2026-09-28)
- Extends: [ADR 0004](ADR-0004-incidents-notifications.md), [ADR 0003](ADR-0003-scheduling-persistence.md),
  [ADR 0002](ADR-0002-persistence.md) (same table, key style, conditional writes, status evaluated on read)

## Context

This record adds signals that StatusForge does not produce itself: job reports (heartbeats) and deployment
markers. Requirements: scoped, revocable credentials; deliberate handling of invalid, duplicate, and stale
reports; honest treatment of periods when StatusForge could not receive; markers that never change health.

Making a heartbeat a monitor kind lets the incident machinery (evaluation inside the counted transaction,
incidents, outbox, reminders, maintenance windows, pause/archive) apply unchanged, provided heartbeat evidence
arrives as counted observations in time order. The design below turns both a report and a missed deadline into
such an observation.

### Access patterns

| # | Operation | Consistency |
| --- | --- | --- |
| H1 | Authenticate a report by monitor ID and token | Strong read of the monitor item |
| H2 | Record a report: duplicate check, eligibility, evaluation, next deadline | Atomic |
| H3 | Record a missed deadline once, or a not-observed gap when StatusForge could not receive | Conditional |
| H4 | Know when StatusForge could not receive (process down or store unavailable) | Durable |
| H5 | Issue, rotate, revoke a token | Conditional |
| H6 | Applications: create, rename, set members, token; list | Strong |
| H7 | Record a deployment marker once per `deploymentId`; list per application and near an incident | Conditional / range query |
| H8 | Activity: recent reports and deployments across the workspace | Fan-out and merge |

## Decisions

### D1. Monitor kind

The monitor item gains `kind` (`http` \| `heartbeat`; absent reads as `http`). An `http` monitor keeps `check` and
`intervalSeconds` exactly as before. A `heartbeat` monitor has no `check`, no `WORK#` items, and instead:

```
heartbeat        map {
  intervalSeconds   number   300 | 900 | 1800 | 3600 | 21600 | 43200 | 86400 | 604800 (+ 10 | 15 | 30 dev)
  graceSeconds      number   60 | 300 | 900 | 1800 | 3600 | 21600 (+ 5 | 10 dev), ≤ intervalSeconds
  token             map { hash (hex SHA-256), hint (last 4 characters), createdAt } — absent when revoked
}
expectation      map {
  reference         time     receipt of the last counted report, or creation / resume / schedule change
  dueAt             time     reference + interval (advanced by interval after each recorded miss or gap)
  lastFinishedAt    time?    newest counted report's finishedAt (for stale detection)
}
```

`kind` is immutable. Changing `heartbeat.intervalSeconds` or `graceSeconds` does not change `configVersion`;
it recomputes `dueAt = reference + newInterval`, or `now` if that deadline plus the new grace has already passed
(the job gets one full grace period), and adds a `config_changed` event to an open incident. The incident
policy default for heartbeats is `{openAfter: 1, recoverAfter: 1}`.

### D2. Heartbeat evidence is an observation

Both reports and missed deadlines are stored as observation items (`MON#<id>` / `OBS#<at>#<observationId>`) with
`kind`:

| `kind` | `at` | `initiatedBy` | `outcome` / `reason` |
| --- | --- | --- | --- |
| `heartbeat_report` | receipt time | `job` | `healthy` / `ok`, or `failing` / `reported_failure` |
| `heartbeat_missed` | `dueAt + grace` | `statusforge` | `failing` / `missing` |

A report additionally stores `report {runId?, finishedAt?, durationMs?, exitCode?, message?, late}` where
`late` means received after `dueAt` but by `dueAt + grace`. Missed observations carry `dueAt`. Both carry
`counted`, `notCountedReason`, and `maintenanceWindowId` with their existing meaning, and pass through
`incident.Evaluate` unchanged; the evidence snapshot gains `kind`. They expire like other observations.

A late report and a report after a miss are ordinary `healthy` evidence; "recovered" is the incident or
headline wording, not a separate outcome.

### D3. Recording a report (H2)

The ingest handler authenticates (D6), validates (see
[heartbeats and deployments](../architecture/heartbeats-and-deployments.md)), then runs one transaction:

- Put `OBS#…` (the report).
- If `runId` is present: put `MON#<id>` / `RUN#<runId>` with `attribute_not_exists` (`expiresAt` = receipt + 7
  days, readers ignore expired). Cancellation on this item alone ⇒ duplicate: nothing is written, `200 duplicate`.
- Monitor update, conditioned on `lifecycle = active`, `kind = heartbeat`, the token hash that authenticated,
  and `evaluation.revision = :rev`:
  - counted path (report `finishedAt` absent or ≥ `expectation.lastFinishedAt`): evaluation updates, and
    `expectation = {reference: receivedAt, dueAt: receivedAt + interval, lastFinishedAt}`; the stored status
    evidence is replaced as in ADR 0003 D5;
  - not-counted path (`older_than_current`): report stored, monitor untouched except `revision`.
- Incident, event, and notification items as in ADR 0004 D3.

A paused monitor gets a not-counted report (`paused`) through a separate transaction conditioned on
`lifecycle = paused`; an archived monitor is refused before writing (`410`). Revision-only conflicts retry up to
3 times as in ADR 0004 D3.

### D4. Deadline step in the scheduler tick (H3)

For each active heartbeat monitor with `dueAt + grace ≤ now`, one transaction:

- Monitor condition `expectation.dueAt = :due AND lifecycle = active` (idempotent: a second tick or process
  finds the deadline moved); set `dueAt = due + interval` (repeat until `dueAt + grace > now`, collapsing any
  backlog into one record, as the HTTP scheduler does for overdue slots).
- If a receive outage (D5) overlaps the report window `(expectation.reference, due + grace]` of the oldest
  collapsed deadline or any later one: put a gap (`GAP#<due>`, reason `not_observed`, `missedCount` = number of
  collapsed deadlines) and clear runs (ADR 0004 D4); no evidence; restart the window at the end of the latest
  overlapping outage: `reference = outage.to`, `dueAt = outage.to + interval`. The job may have tried to report
  during the outage, so it gets a full window after StatusForge is receiving again. (This rule was added after
  a smoke run showed a deadline 4 s after an outage being judged missing.)
- Otherwise: put one `heartbeat_missed` observation for the newest collapsed deadline and apply evaluation;
  if older deadlines were collapsed (a tick stall shorter than the outage threshold, or several short intervals
  in dev), put one gap for them with reason `overdue` and their count, as the HTTP scheduler does for skipped
  slots.

The step runs only when the liveness record (D5) shows `aliveAt ≥ due + grace`, so an outage is always known
before a deadline inside it is judged.

### D5. Receive liveness (H4)

One item `SYSTEM` / `LIVENESS`:

```
aliveAt       time    last successful liveness write
outages       list    up to 50 most recent {from, to} periods when StatusForge could not receive, oldest dropped
```

The scheduler writes it every 10 s (injected clock). On a successful write, if the previous `aliveAt` is more than
30 s old, the period `(previous aliveAt, now)` is appended to `outages` in the same write. Process restarts
therefore create an outage from the last write before shutdown or crash to the first write after start; a
database outage that stops writes creates one too, because reports could not be stored during it (the ingest
route returns `503`). A deadline `d` is inside an outage when `from < d ≤ to` for any listed period.

Rejected: per-monitor "checked through" writes every tick (write volume grows with monitors); treating process
start time alone as the outage end (misses database outages).

Limits: outages shorter than 30 s (3 × the liveness interval) are not detected; after an outage a job that has
really stopped is reported one full interval after StatusForge is receiving again. Both are stated in the
architecture documentation.

### D6. Tokens (H1, H5)

Format `sfh_` + 32 random bytes in base32 (heartbeat) and `sfd_` + the same (application). Only the SHA-256
hex digest and the last 4 characters are stored. Authentication compares digests in constant time. Issue and
rotate replace `token` with a condition on the previous hash (or its absence); revoke removes it. The plaintext is
returned only in the issuing response and never logged; request logging omits the `Authorization` header.

Rate limiting is an in-process token bucket per monitor or application ID (1 per second, burst 5), applied after
authentication so unauthenticated floods cannot drain a real job's allowance; failed authentication is limited
per remote address the same way.

### D7. Pause, resume, maintenance

Pause stops the deadline step for the monitor. Resume sets `expectation.reference = now` and `dueAt = now +
interval` in the resume transaction. Maintenance windows (ADR 0004 D11) label reports by receipt time and missed
deadlines by `due + grace`; labelled evidence is excluded from evaluation.

### D8. Applications and deployments

| Entity | `PK` | `SK` |
| --- | --- | --- |
| Application | `APPLICATIONS` | `APP#<applicationId>` |
| Deployment marker | `APP#<applicationId>` | `DEP#<reportedAt>#<deploymentMarkerId>` |
| Deployment ID guard | `APP#<applicationId>` | `DEPID#<deploymentId>` |

```
Application   entityType "application", applicationId, name (1–100), token? {hash, hint, createdAt},
              createdAt, updatedAt, archivedAt?
Monitor       applicationId?   (at most one application per monitor or heartbeat)
Marker        entityType "deployment", markerId, applicationId, version, description?, link?, deployedAt?,
              deploymentId?, source ("ingest" | "manual"), reportedAt
```

Membership lives on the monitor item (set and cleared with a condition on the previous value), so the monitor list
already carries it and no membership list can drift. A marker put and its `DEPID#` guard share a transaction;
cancellation on the guard alone ⇒ duplicate. Markers are never updated or deleted. "Nearby deployments" for an
incident is one range query on `DEP#` between `openedAt − 2 h` and `resolvedAt` (or now). Markers are not read
by evaluation, status, or notifications.

The incident item records the monitor's `applicationId` when it opens (the evaluation transaction already holds
the monitor item), and nearby deployments use that application, falling back to the monitor's current one for
incidents opened without membership. Otherwise archiving an application, which clears membership, would hide
markers from past incidents. Membership changes do not bump `configVersion`: they change context, not how the
monitor is checked.

### D9. Activity feed (H8)

Recent reports and deployments are read by per-monitor (`OBS#` of heartbeat monitors, newest first, `limit`) and
per-application (`DEP#`, newest first, `limit`) queries merged by time, the same bounded fan-out accepted in ADR
0003 D1 and ADR 0004 D1.

### D10. Package boundaries

- `internal/heartbeat` (stdlib only): schedule values, deadline math, late/missing classification, report
  validation, outage containment, token format and hashing helpers.
- `internal/store`: D3–D8 transactions; typed errors.
- `internal/scheduler`: deadline step and liveness writer.
- `internal/httpapi`: `/ingest/…` routes (separate middleware: token auth, size limit, rate limit, no CORS) and
  management routes.
- `internal/samplejob` + `cmd/sample-job`: fixture only; never imported by the product.

## Alternatives considered

- **Heartbeats as a separate entity** — would duplicate the incident machinery.
- **Missed windows as gaps rather than evidence** — would never open an incident, which is the point of a
  heartbeat monitor.
- **Deadline work items like `WORK#`** — one moving deadline per monitor needs no queue; the conditional
  cursor on the monitor item is the reminder pattern from ADR 0004 D6.
- **Token in the URL** — rejected; tokens travel only in the `Authorization` header.

## Consequences

- Monitor reads and writes branch on `kind`; behaviour of `http` monitors is unchanged.
- Observation readers accept `kind` and report fields; the web does not assume every observation has a request.
- Coverage summaries ([ADR 0006](ADR-0006-summaries-overview.md)) report `not_observed` gaps and outage
  periods directly.
- Ingest must never move to a non-loopback listener without an explicit exposure design; token hashing is
  kept on every path.
