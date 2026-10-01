# ADR 0003: Scheduling persistence — work items, leases, counted status, gaps

- Status: accepted (2026-09-27)
- Extends: [ADR 0002](ADR-0002-persistence.md) (same table, key scheme, conditional-write style).
  Supersedes ADR 0002 D6 "no denormalised last-observation summary" and D7 "single flight in-process".

## Context

Scheduling turns manual checks into a background workflow: a scheduler creates due work, a bounded pool
executes it, and the result may update a current status. The requirements: interruption never silently
loses an obligation; old, duplicate, or obsolete results cannot overwrite newer state; monitoring gaps stay
visible. One API process runs the scheduler and the pool, but the design must still be correct if two
processes ran against the same table (which the [cloud path](ADR-0008-cloud-path.md) later does), because
every state change is conditional.

### Access patterns

| # | Operation | Consistency need |
| --- | --- | --- |
| B1 | Scheduler: list active monitors with their schedule cursor | Strong; once per tick |
| B2 | Scheduler: create the work item for a due slot exactly once | Idempotent |
| B3 | Scheduler: record missed slots as one gap and advance the cursor exactly once | Conditional on the cursor |
| B4 | Worker: find claimable work for a monitor (pending, or claimed with an expired lease) | Strong |
| B5 | Worker / manual check: acquire the per-monitor lease | Conditional; one holder |
| B6 | Worker / manual check: record the result, update status if eligible, release the lease | Atomic per branch |
| B7 | Pause, resume, archive, check-setting change: cancel or create work | Conditional |
| B8 | Read status, observations, gaps for a monitor | Strong |

## Decisions

### D1. New items in the existing table

| Entity | `PK` | `SK` |
| --- | --- | --- |
| Monitor (extended) | `MONITORS` | `MON#<monitorId>` |
| Observation (extended) | `MON#<monitorId>` | `OBS#<startedAt>#<observationId>` |
| Work item | `MON#<monitorId>` | `WORK#<dueAt>` |
| Gap | `MON#<monitorId>` | `GAP#<fromDueAt>` |

`dueAt` uses the fixed-width UTC sort layout from ADR 0002 D2. One work item per monitor per due instant: the
key is the idempotency key, so a repeated scheduler tick, a restart, or a second process cannot create a
duplicate. Immediate runs (create, resume, check-setting change) use `dueAt` = the request time, which cannot
collide with a grid slot except by exact nanosecond coincidence, in which case the existing item serves both.
Keys keep nanosecond precision for this reason; the API serialises all times as UTC milliseconds.

No GSI. The scheduler reads the monitor list (one partition) and, per active monitor, queries a bounded
`WORK#` range (B4). With the handful of monitors this product targets that is cheaper than a second index
and stays strongly consistent. The cloud adapter replaces this dispatch path, not the item model.

### D2. Monitor item additions

```
intervalSeconds     number   60 | 300 | 600 | 900, or 10 | 15 | 30 when unlocked by the dev minimum
scheduledThrough    string   newest grid slot for which work or a gap has been recorded (sort layout)
lease               map, present only while held {
  token               string   random per acquisition
  until               string   RFC 3339 UTC; deadlineMs + 10 s after acquisition
  kind                "scheduled" | "manual"
}
status              map, present once a counted observation exists {
  observationId, initiatedBy, startedAt, completedAt, outcome, reason, observedStatus?, configVersion
}
```

The per-monitor offset is **not stored**: it is `fnv32a(monitorId) mod intervalMs` (whole milliseconds), so it is
deterministic and changes consistently with the interval. Grid slot *k* is `k × interval + offset` since the
Unix epoch. The monitor item also carries `lastClaimAt`, set in the same write as every lease acquisition
(scheduled or manual), for fair dispatch (D3).

### D3. Scheduler tick (B1–B3)

Every 2 s (injected clock in tests), for each monitor with `lifecycle = active`:

1. `latest` = newest grid slot ≤ now. If `latest ≤ scheduledThrough`, nothing to do.
2. Missed slots are those strictly between `scheduledThrough` and `latest`. If any exist, write a gap
   `{fromDueAt, toDueAt, missedCount, reason: "not_scheduled"}`.
3. Put the work item for `latest` with `attribute_not_exists(SK)`.
4. Advance `scheduledThrough = latest` on the monitor with condition `scheduledThrough = :previous AND
   lifecycle = "active"`.

Steps 2–4 are one `TransactWriteItems`, so a gap, its work item, and the cursor move together or not at all.
A lost race (another tick or process already advanced the cursor) cancels the transaction and is ignored.
When a monitor is created or resumed, `scheduledThrough` is set to the newest slot ≤ now in the same write, so
the time before creation and the time spent paused are never gaps.

Each tick queries only `WORK#` items due within `interval + 50 s` of now (the longest time an item can stay
open under the rules), and closes overdue or exhausted items itself. A startup sweep covers the retained
history once; after a failed tick for a monitor, the next successful tick widens its query back to the last
successful tick. Cost therefore does not grow with history.

Work is dispatched to the pool through a bounded channel whose queued plus in-flight total never exceeds the
worker count. Candidates are ordered by the monitor's oldest `lastClaimAt`, then oldest `dueAt`, so saturation
spreads missed slots across monitors instead of starving the one with the latest offset. A full pool leaves
work `pending`; the next tick offers it again. Work not claimed before `dueAt + interval` is marked `missed`
with reason `overdue` and a single-slot gap is written in the same transaction.

### D4. Work item and lease (B4, B5)

```
entityType      "work"
monitorId, dueAt
trigger         "schedule" | "create" | "resume" | "config_change"
configVersion   number     monitor version when the work was created
state           "pending" | "claimed" | "done" | "cancelled" | "missed"
attempts        number     0–2
claimToken      string, while claimed
leaseUntil      string, while claimed
outcomeRef      observationId, when done
closedReason    "completed" | "paused" | "archived" | "config_changed" | "overdue" | "lease_expired"
expiresAt       number     dueAt + 7 days
```

Claiming is one transaction:

- Work item: `state = "pending" OR (state = "claimed" AND leaseUntil < :now AND attempts < 2)`;
  sets `state = claimed`, `claimToken`, `leaseUntil`, `attempts + 1`.
- Monitor: `lifecycle = "active" AND configVersion = :workVersion AND (attribute_not_exists(lease) OR
  lease.until < :now)`; sets `lease`.

If the monitor part fails because the monitor is paused, archived, or on a newer version, the worker closes
the work item as `cancelled` with the matching `closedReason` (conditional on the state it read). If it fails
because another lease is live, the work stays pending and is retried on a later tick.

A claimed item whose lease expired with `attempts = 2`, or whose retry would start after `dueAt + interval`,
is closed as `missed` / `lease_expired` with a single-slot gap. Every due slot therefore ends as exactly one
of: `done` (with an observation), `cancelled` (with a reason), or `missed` (with a gap).

Manual checks acquire the same monitor lease (condition `attribute_not_exists(lease) OR lease.until < :now`,
plus `lifecycle <> "archived"`; paused is allowed). This replaces the in-process lock from ADR 0002 D7, and a
held lease of either kind produces `409 check_in_progress`.

### D5. Recording a result (B6)

The checker runs under its own deadline context. The result is recorded by trying two transactions in order:

**Counted:**
- Monitor: `lease.token = :token AND lifecycle = "active" AND configVersion = :v AND
  (attribute_not_exists(status) OR status.startedAt < :startedAt)`; sets `status`, removes `lease`.
- Observation: `attribute_not_exists(SK)`, with `counted = true`.
- Work item (scheduled only): `claimToken = :token`; sets `state = done`, `outcomeRef`.

**Not counted** (only if the counted transaction was cancelled by the monitor condition):
- Monitor: `lease.token = :token`; removes `lease`. If this condition also fails, the lease was lost and the
  monitor item is not touched.
- Observation: `attribute_not_exists(SK)`, with `counted = false` and `notCountedReason` derived from the
  monitor item read after the cancellation: `paused`, `archived`, `config_changed`, `older_than_current`, or
  `lease_lost`.
- Work item (scheduled only): `claimToken = :token`; sets `state = done`. If the token no longer matches (a
  retry took over), the work item is left alone.

The observation is always written, so no result is lost; only the status update is conditional. The
`status.startedAt < :startedAt` guard means a slow old result finishing after a newer one never replaces it.

### D6. Lifecycle and configuration writes (B7)

- **Pause / archive:** unchanged conditions from ADR 0002 D5. No work items are touched eagerly; pending
  work is cancelled when a worker next tries to claim it (D4), which avoids scanning work on every pause.
- **Resume:** sets `lifecycle = active`, `scheduledThrough` = newest slot ≤ now, and puts a `resume` work item
  at `dueAt = now`, in one transaction.
- **Check-setting change (version bump):** the existing conditional update, plus a `config_change` work item
  at `dueAt = now` in the same transaction. Old-version pending work is cancelled at claim time.
- **Interval change:** no version bump; sets `intervalSeconds` and resets `scheduledThrough` to the newest
  slot ≤ now on the new grid (no gap for the switch).
- **Create:** monitor item plus a `create` work item at `dueAt = now`.

### D7. Current status is evaluated on read

The stored `status` is evidence, not a verdict. The monitor domain package computes the presented state from
the monitor item and the clock:

| Condition (first match) | Presented state |
| --- | --- |
| `lifecycle = archived` | `archived` |
| `lifecycle = paused` | `paused` |
| no `status` | `unknown` / `no_checks` |
| `status.configVersion < configVersion` | `unknown` / `config_changed` |
| `now − status.completedAt > 2 × interval + deadline` | `stale` |
| otherwise | `status.outcome` (`healthy`, `failing`, `checker_problem`) |

`freshUntil = status.completedAt + 2 × interval + deadline` is returned so a client can switch to stale
without another request. Nothing stores `stale` or `unknown`.

### D8. Gaps

```
entityType   "gap"
monitorId, fromDueAt, toDueAt, missedCount
reason       "not_scheduled" | "overdue" | "lease_expired"
recordedAt, expiresAt (recordedAt + 90 days)
```

Gaps are append-only and never merged after the fact; two adjacent gaps are shown as two rows.

### D9. Shutdown and restart

On SIGINT/SIGTERM the scheduler stops ticking, workers stop claiming, and in-flight checks get up to 30 s to
record. Anything still claimed is recovered through lease expiry (D4) after restart, so a crash and a clean
stop use the same recovery path; both are exercised by the scheduler and store tests.

### D10. Package boundaries

- `internal/monitor`: grid math, interval validation, status evaluation (D7), not-counted reasons. Stdlib only.
- `internal/scheduler`: tick loop, dispatch channel, worker pool, shutdown. Depends on small interfaces, not
  on the store type, so tests run with a fake store and fake clock.
- `internal/store`: the transactions above; typed errors (`ErrLeaseHeld`, `ErrNotEligible`, …). No SDK types
  leak.

## Alternatives considered

- **In-memory queue, recompute obligations on restart.** Rejected: a crash loses the record that work was
  due and in flight.
- **Sparse GSI of pending work.** Eventually consistent in the cloud and an extra resource; per-monitor queries
  suffice at this scale.
- **Separate status item (`MON#<id>` / `STATUS`).** Would move the eligibility check (version, lifecycle)
  to a different item from the facts it depends on; keeping `status` on the monitor makes D5 one condition.
- **Storing `stale` or `unknown`.** Rejected; they are functions of time and configuration (D7).
- **Eager cancellation of work on pause or reconfiguration.** Needs a scan of `WORK#` items in the same write;
  claim-time cancellation gives the same outcome with a smaller write.
- **Merging adjacent gaps.** Mutating history; rejected.

## Consequences

- The list response carries `status` instead of a last observation; the web migrated in the same change.
- Incident logic ([ADR 0004](ADR-0004-incidents-notifications.md)) consumes counted observations only and
  can rely on their order.
- The cloud path ([ADR 0008](ADR-0008-cloud-path.md)) reproduces D3–D5 with a queue for dispatch; the item
  model and conditions carry over.
