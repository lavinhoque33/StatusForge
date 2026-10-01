# ADR 0007: Retention and TTL, permanent deletion, export/import, data format, local release

- Status: accepted (2026-09-29)
- Extends: [ADR 0006](ADR-0006-summaries-overview.md), [ADR 0005](ADR-0005-heartbeats-deployments.md),
  [ADR 0004](ADR-0004-incidents-notifications.md), [ADR 0003](ADR-0003-scheduling-persistence.md),
  [ADR 0002](ADR-0002-persistence.md) (same table and key style; conditional writes; status evaluated on read).
  Supersedes ADR 0002 D8 "TTL is not enabled" and the earlier absence of any deletion path.

## Context

A maintainable local release needs: retention and deletion behaviour, export or recovery, upgrade validation
with preserved data, bounded capacity evidence, security and dependency review, reproducible sample data, and
a README a fresh contributor can follow. Retention must be finite, protect active work and unresolved
obligations, be honoured by readers without assuming instant physical expiry, and be stated by summaries when
underlying detail has expired.

State before this record:

- Only observations `OBS#` and gaps `GAP#` (+90 d) and work items `WORK#` and heartbeat run guards `RUN#` (+7 d)
  carried `expiresAt`, always as a Number (Unix seconds). TTL was not enabled; `Store.Observations`, `Store.Gaps`,
  the summary, and the history readers skipped expired rows.
- Incidents `INC#`, their events, notifications, and attempts (`INCX#…`), lifecycle events `LIFE#`, maintenance
  windows `MAINT#`, deployment markers `DEP#` and guards `DEPID#`, attention pointers, monitors, and applications
  never expired. The only deletion path was `make db-reset`.
- Nothing read `WORK#.expiresAt`; all work queries were bounded to the last 7 days. `RUN#` was only written,
  with the condition `attribute_not_exists(PK) OR expiresAt < :now`. Physically deleting either after expiry
  changes no behaviour.
- Incidents are `open` → `resolved` only; a later failure opens a new incident. Resolved incidents are never
  mutated. Incident evidence is stored as snapshots; no reader looks up an observation by ID.
- **DynamoDB Local 3.3.1 performs TTL deletion** (probed 2026-09-28): after `UpdateTimeToLive` on `expiresAt`,
  Number-typed past values were deleted within 35–63 s; future values, String values, and items without the
  attribute were kept. Hosted DynamoDB deletes expired items typically within days.
- No schema version, migration, export, import, backup, or version information existed.

## Decisions

### D1. Retention periods

Fixed, not configurable. `expiresAt` is always a Number (Unix seconds).

| Record | `expiresAt` | Set when |
| --- | --- | --- |
| Observation `OBS#`, gap `GAP#` | start / recorded + 90 d | write (unchanged) |
| Work item `WORK#`, run guard `RUN#` | due / receipt + 7 d | write (unchanged) |
| Lifecycle event `LIFE#` | `at` + 365 d | write |
| Maintenance window `MAINT#` | effective end + 365 d (effective end = `endAt`, or the early end when an active window is cancelled) | create; rewritten by cancel |
| Deployment marker `DEP#`, guard `DEPID#` | `reportedAt` + 365 d | write |
| Incident `INC#`, events, notifications, attempts (`INCX#…`), its `ATTENTION#` pointers | `resolvedAt` + 365 d (`retainUntil`) | by the retention job (D3), only after resolution and only when none of its notifications is `pending`, `retry_wait`, or `sending` |
| Monitor `MON#`, application `APP#`, `NAME#`, `SYSTEM` items, `DUE#` pointers, jobs | never | — |

Protected: open incidents and their children carry no `expiresAt`; a non-final notification keeps its incident
and siblings unstamped; active and future maintenance windows have an effective end in the future; `DUE#`
pointers never expire. The `DEPID#` duplicate-guard condition becomes
`attribute_not_exists(PK) OR expiresAt < :now`, like `RUN#`.

Readers of every expiring type skip rows whose `expiresAt` has passed (items without `expiresAt` never expire,
except where a reader already requires it: observations and gaps). Physical deletion never changes an API result;
it only removes rows readers already skip.

### D2. Physical deletion by DynamoDB TTL

`Store.Initialize` calls `DescribeTimeToLive` and, when TTL is disabled, `UpdateTimeToLive` (`expiresAt`,
enabled). TTL enabled on a different attribute is a startup error that names the table and attribute. The TTL
status is reported by `GET /api/system`. Readers keep filtering (D1) because deletion is asynchronous (seconds on
DynamoDB Local, typically days on hosted DynamoDB). Hosted behaviour, IAM permission for `UpdateTimeToLive`, and
the once-per-hour change limit belong to the [cloud path](ADR-0008-cloud-path.md) and are not claimed here.

### D3. Incident retention job

The three resolution sites (recovery in `evaluationItems`, archive in `Store.save`, heartbeat archive in
`saveHeartbeat`) add one item to their existing transaction: a job pointer `PK JOBS`, `SK
JOB#retain#<monitorId>#<incidentId>`. The housekeeping worker (D8) processes it:

1. Query the incident's children (`INCX#<incidentId>#`). If any notification is non-final, leave the job for the
   next run.
2. Set `expiresAt = retainUntil` on every child and on `ATTENTION#<monitorId>#<incidentId>#…` pointers, then on
   the `INC#` item last, then delete the job. Each step is idempotent (the value is deterministic), so a crash
   repeats work without changing results.

A manual notification retry (`failed` → `pending`) removes `expiresAt` from that notification and the `INC#` item
and re-puts the job pointer in the same transaction; the job later stamps them again with the same
`retainUntil`.

### D4. Permanent deletion of archived monitors and applications

Only archived monitors and archived applications can be deleted. `POST …/deletion {"confirmName"}` checks the
exact current name on the server, then in one transaction sets `deletion {state, requestedAt, removedItems,
updatedAt}` on the item (condition: archived, no deletion yet) and puts `JOB#delete-monitor#<id>` or
`JOB#delete-application#<id>`. The housekeeping worker:

- **Monitor:** waits while any of its notifications is non-final (state `waiting_for_notifications`); then
  deletes its `DELIVERY` pointers (`DUE#…#<monitorId>#…` found by querying the `DELIVERY` partition and filtering
  on the monitor ID segment, `ATTENTION#<monitorId>#…` by prefix) and every item in `MON#<monitorId>` in batches
  of at most 25, updating `removedItems` after each batch; re-queries until the partition is empty; finally
  deletes the `MON#` item and the job in one transaction (condition: `deletion` present).
- **Application:** deletes `APP#<appId>` (markers and guards) in batches the same way, then the `APP#` item and
  the job. Archive already removed the name guard, the token, and memberships.

While a monitor is being deleted every mutation returns `409 deleting`; the scheduler, delivery, heartbeat
ingest, and retention job skip it; any write path that could recreate an item in its partition is conditioned on
the monitor existing without `deletion`. Reads show the monitor with its deletion progress; after completion the
monitor is `404`. Incidents keep `applicationId` snapshots; readers show a missing application as "Deleted
application" instead of failing. Deletion survives restarts because the job and the progress live in the table.

### D5. Export and import

Subcommands of the `statusforge` binary, using the server's configuration and loopback guard:

- `statusforge export [--table NAME] [--out PATH]` scans the table and writes JSON Lines: a header
  `{"format":"statusforge-export","exportVersion":1,"table","dataFormat","appVersion","startedAt"}`, one line per
  item in DynamoDB JSON (type-tagged, lossless), and a trailer `{"trailer":true,"items":N,"sha256":"<hex of the
  item lines>","finishedAt"}`. File mode `0600`; an existing file is never overwritten; the default path is under
  `.local/exports/` (git-ignored). A scan of a table in use is not a snapshot: the command warns when the liveness
  item shows the API alive, and the runbook says to stop the API first.
- `statusforge import --in PATH --table NAME` validates the whole file (header, every line, count, digest) before
  writing anything, refuses a table that contains any item, creates the table through `Store.Initialize` (so TTL
  is enabled), and writes with batch retries for unprocessed items. Items are imported unchanged, including
  `expiresAt`; already-expired items are then removed by TTL.

Exports contain token hashes (never raw tokens) and must be treated as private local data.

### D6. Data format marker and upgrade

`PK SYSTEM`, `SK FORMAT`: `{dataFormat: 6, writtenBy, firstWrittenAt, updatedAt, upgradedFrom, backfill {state,
cursor, stamped, startedAt, finishedAt}}`. At startup:

- no marker and an empty table → write `dataFormat 6`, `backfill.state "done"`;
- no marker and existing items (data written by earlier builds) → write the marker with `upgradedFrom "unmarked"`
  and start the backfill; `dataFormat` greater than 6 → the server refuses to start and names both formats;
- every start updates `writtenBy` and `updatedAt`.

The backfill is the only data rewrite this record introduces. The housekeeping worker scans the table in pages,
storing the scan cursor in the marker so a restart resumes: `LIFE#`, `MAINT#`, `DEP#`, `DEPID#` without `expiresAt`
get the D1 value; resolved `INC#` items without `expiresAt` get a D3 job pointer. Everything else keeps the
read-time fallbacks for earlier item shapes (`legacy.go`, ADR 0006 D3). Upgrade evidence uses datasets written
by earlier builds, captured with the export command and committed as test fixtures
(`backend/internal/upgrade/testdata/`).

### D7. Local API protection

- `Host` must name a loopback host (`127.0.0.1`, `localhost`, `[::1]`, any port) on every route → otherwise
  `421 host_not_allowed` (DNS-rebinding defence).
- State-changing `/api` requests (`POST`, `PUT`, `PATCH`, `DELETE`): a present `Origin` must be a loopback origin
  and `Sec-Fetch-Site: cross-site` is refused → `403 origin_not_allowed`; a body requires
  `Content-Type: application/json` → `415 unsupported_media_type`; bodies over 64 KiB → `413 too_large`.
- The served web page gets a CSP (`default-src 'self'`, no inline script, `frame-ancestors 'none'`,
  `object-src 'none'`, `base-uri 'none'`) plus the `/api` headers where they apply. Ingest keeps its token and
  4 KiB limits.

The management API stays unauthenticated because it is loopback-only; any shared or hosted interface needs an
authentication design first.

### D8. Housekeeping worker

One goroutine in the API process, independent of `STATUSFORGE_SCHEDULER_ENABLED`, every
`STATUSFORGE_HOUSEKEEPING_INTERVAL_SECONDS` (default 60, range 2–3600): format backfill, then retention jobs, then
deletion jobs, each bounded per run. Jobs are idempotent, so two processes would repeat work without changing
results (one process remains the supported model).

### D9. Release shape

- One Go binary serves the built web (embedded at build time) on `127.0.0.1:8080` with history fallback for
  non-`/api`, non-`/ingest` paths; DynamoDB Local stays in Compose. `make build`, `make run`.
- Version from `-ldflags` (`git describe`), falling back to the VCS revision, then `dev`; shown by
  `statusforge version`, `GET /api/system`, and the Settings page.
- `make security-check` (also in CI): Go vulnerability check, `npm audit` on production dependencies, a licence
  allowlist, and a secret scan, all pinned and free. A written [security review](../operations/security-review.md)
  records tested controls and gaps.
- `make demo` seeds 7 days of synthetic history through the store's own write paths with a simulated clock into
  `statusforge_demo` (refusing any other table), marks the table as demo data, and runs the fixtures live.
- Capacity evidence is a recorded loopback run ([capacity report](../operations/capacity-report.md)).

## Alternatives considered

- **In-app cleanup job instead of TTL:** works identically on every backend, but duplicates a DynamoDB feature;
  TTL was chosen once local testing proved it works on DynamoDB Local.
- **Stamping incident children at write time:** impossible for items written while the incident is open, because
  the resolution time is unknown; stamping the `INC#` item in the resolution transaction would let it expire
  while a notification is still pending.
- **Migration framework:** heavier than one backfill; read-time fallbacks already cover earlier item shapes.
- **Copying DynamoDB Local data files as backup:** tied to its storage format and version; documented only as an
  operator option, not the supported path.
- **Stored rollups:** rejected; 24 h and 7 d windows stay within the 90-day detail.

## Consequences

- Every record type has a stated lifetime; storage is bounded apart from monitors, applications, and their
  configuration.
- Incident history older than 90 days keeps its snapshots but not the observations and gaps around it; incident
  detail says so.
- Deletion is permanent; export is the recovery path and the runbook says to export first.
- Older binaries do not read the format marker; the marker protects against a newer format being opened by this
  build and later ones, not against earlier builds.
- DynamoDB Local proves TTL mechanics only; hosted timing and permissions remain unverified.
