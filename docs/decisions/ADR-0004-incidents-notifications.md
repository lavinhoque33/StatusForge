# ADR 0004: Incident evaluation, incidents, notification outbox, maintenance windows

- Status: accepted (2026-09-28)
- Extends: [ADR 0003](ADR-0003-scheduling-persistence.md) and [ADR 0002](ADR-0002-persistence.md) (same
  table, key style, conditional writes, status evaluated on read)

## Context

This record turns counted observations into incidents and incidents into notifications delivered to a
receiver. Requirements: old or ineligible evidence cannot change incident state; repeated failures do not
multiply incidents; recovery keeps history; a notification obligation is never held only in memory; delivery
may be retried after an unknown outcome and is never claimed to be exactly once.

The scheduling design already gives one serialisation point per monitor: only the holder of the monitor
lease writes a counted result, inside a transaction conditioned on the monitor item (ADR 0003 D5). Incident
evaluation joins that transaction, so incident state can never disagree with the evidence that produced it.

### Access patterns

| # | Operation | Consistency |
| --- | --- | --- |
| C1 | Evaluate a counted result: update runs; open, extend, or resolve an incident; record notification intent | Atomic with the result |
| C2 | Reset runs when a gap is recorded | Atomic with the gap |
| C3 | Create a reminder when due, at most once per slot | Conditional |
| C4 | Delivery worker: find due notifications, claim one, record the attempt and the next state | Conditional, lease-based |
| C5 | List open incidents across monitors; list recent incidents across monitors and per monitor | Strong |
| C6 | Read an incident with its events, notifications, attempts, and gaps | Strong |
| C7 | List notifications that need attention; manually retry one | Strong / conditional |

## Decisions

### D1. Keys

| Entity | `PK` | `SK` |
| --- | --- | --- |
| Incident | `MON#<monitorId>` | `INC#<incidentId>` |
| Incident event | `MON#<monitorId>` | `INCX#<incidentId>#EV#<at>#<seq>` |
| Notification | `MON#<monitorId>` | `INCX#<incidentId>#NOTE#<noteKey>` |
| Delivery attempt | `MON#<monitorId>` | `INCX#<incidentId>#NOTE#<noteKey>#ATT#<nn>` |
| Due pointer | `DELIVERY` | `DUE#<nextAttemptAt>#<monitorId>#<incidentId>#<noteKey>` |
| Attention pointer | `DELIVERY` | `ATTENTION#<monitorId>#<incidentId>#<noteKey>` |

`incidentId` is time-ordered and opaque: the opening time in the compact fixed-width form
`20260928T101530123Z` followed by `-` and 6 random base32 characters. `INC#` queries in descending order list a
monitor's incidents newest first without touching the `INCX#` children, and one `begins_with INCX#<id>` query
returns an incident's full detail. `noteKey` is `opened`, `reminder#<nnnn>` (slot number from 0001), or
`resolved`; the key is the idempotency key, so a transition or reminder can never produce two notifications.

The `DELIVERY` partition holds only pointers to work that still needs action; delivered notifications leave it.
Its size is bounded by undelivered and failed notifications, not by history.

No GSI. Open incidents come from the monitor list (D2), which the scheduler already reads every tick. The
cross-monitor "recent incidents" list queries each monitor's `INC#` range with the page limit and merges; this is
the same per-monitor fan-out accepted in ADR 0003 D1.

### D2. Monitor item additions

```
incidentPolicy   map { openAfter: 1–5 (default 2), recoverAfter: 1–5 (default 2) }
evaluation       map {
  revision         number   +1 on every write to this map
  failRun          list     up to 5 evidence snapshots of consecutive counted failing observations
  healthyRun       list     up to 5 evidence snapshots of consecutive counted healthy observations (open incident only)
}
openIncident     map, present while an incident is open {
  id, openedAt, nextReminderAt, reminderSeq
}
```

An **evidence snapshot** is `{observationId, startedAt, initiatedBy, outcome, reason, observedStatus?,
configVersion}`. Incidents store snapshots, not only observation IDs, so they stay interpretable after
observations expire.

Changing `incidentPolicy` does not increment `configVersion` and keeps the current runs; the new thresholds apply
from the next evaluation.

### D3. Evaluation joins the counted-result transaction (C1)

The pure function `incident.Evaluate(policy, evaluation, openIncident, observation, now)` in the incident
domain package returns the next `evaluation`, an optional transition, and the notification intents to write.
The counted transaction from ADR 0003 D5 gains:

- Monitor condition `evaluation.revision = :rev` (absent revision reads as 0) in addition to the existing
  eligibility conditions, and the updates to `evaluation`, `openIncident`.
- **Open** (no open incident, `failRun` reaches `openAfter`): put the incident (`attribute_not_exists`), put the
  `opened` event, put the `opened` notification and its due pointer; set `openIncident` with `nextReminderAt =
  openedAt + reminder interval`, `reminderSeq = 0`; clear both runs.
- **Extend** (open incident, failing): update the incident (`failureCount + 1`, `lastFailure` snapshot) with
  condition `state = "open"`; clear `healthyRun`.
- **Checker problem** (open or not): update `checkerProblemCount` and `lastCheckerProblem` on the open incident
  if any; runs unchanged.
- **Resolve** (open incident, `healthyRun` reaches `recoverAfter`): update the incident to `resolved`
  (`resolution = "recovered"`, `resolvedAt`, recovery evidence) with condition `state = "open"`; put the
  `resolved` event, the `resolved` notification and its due pointer; remove `openIncident`; clear both runs.

If the transaction is cancelled only because `evaluation.revision` moved (a gap or lifecycle write raced it),
the recorder re-reads the monitor and retries the counted path up to 3 times; other cancellation reasons keep the
ADR 0003 not-counted path. Only the lease holder writes counted results, so revision races are rare and bounded.

### D4. Gaps reset runs (C2)

Every transaction that writes a gap (ADR 0003 D3 tick, overdue and lease-expired closures) also updates the
monitor: clear `failRun` and `healthyRun`, `revision + 1`. When an incident is open, the gap is shown on its
timeline by reading `GAP#` items between `openedAt` and `resolvedAt` (or now); no copy is stored.

### D5. Lifecycle and configuration writes

With an open incident, the existing monitor writes add, in the same transaction:

- **Pause / resume:** a `paused` or `resumed` event. Resume also sets `openIncident.nextReminderAt` to the next
  reminder slot after now (D6). Runs are cleared on resume (paused time is not evidence).
- **Check-setting change:** a `config_changed` event with old and new version; runs are cleared.
- **Archive:** the incident becomes `resolved` with `resolution = "archived"`, plus a `resolved` event carrying
  that resolution, the `resolved` notification and its due pointer; `openIncident` is removed. Undelivered
  reminders are then cancelled in a separate best-effort step (D7 claim-time cancellation remains the fallback);
  the same step follows a `recovered` resolution.

Each of these uses condition `openIncident.id = :id` (or `attribute_not_exists(openIncident)` when none was read),
so an incident opened concurrently is never missed.

### D6. Reminders (C3)

Reminder slot *k* is `openedAt + k × STATUSFORGE_REMINDER_INTERVAL_SECONDS`. In each scheduler tick, for each
active monitor whose `openIncident.nextReminderAt ≤ now`, one transaction:

- Monitor: condition `lifecycle = "active" AND openIncident.id = :id AND openIncident.nextReminderAt = :due`;
  set `nextReminderAt` to the first slot **after now** and `reminderSeq + 1`.
- Put the `reminder#<seq>` notification (`attribute_not_exists`) and its due pointer.

Missed slots (downtime, backlog) collapse into this single reminder. Paused monitors are skipped by the tick;
resume moves `nextReminderAt` past now (D5), so pause never produces a late reminder.

### D7. Notification and delivery attempts (C4)

```
Notification
  entityType "notification", monitorId, incidentId, noteKey, kind ("opened" | "reminder" | "resolved")
  payload           the exact JSON body to send (built once at intent time)
  state             "pending" | "retry_wait" | "sending" | "delivered" | "failed" | "cancelled"
  attempts          number
  nextAttemptAt     string, while pending or retry_wait
  lease             { token, until } while sending
  lastResult        see attempt.result
  deliveredAt / failedAt / cancelledReason

Attempt
  entityType "attempt", attempt number, startedAt, completedAt?, manual (bool)
  result   "in_flight" | "delivered" | "http_error" | "rejected" | "timeout" | "connection_refused"
           | "connection_error" | "outcome_unknown" | "refused_by_policy" | "process_stopped"
  httpStatus?, durationMs?
```

The delivery worker polls `DELIVERY` `DUE#` ≤ now every 2 s (limit 10) and, per candidate, runs one claim
transaction: notification condition `state IN ("pending","retry_wait") OR (state = "sending" AND lease.until <
:now)`; sets `state = sending`, a new lease (`until = now + 15 s`), `attempts + 1`; puts the attempt as
`in_flight`. If the claim takes over an expired `sending` lease, the previous `in_flight` attempt is updated to
`process_stopped` in the same transaction.

Before claiming, the worker applies ordering and cancellation rules from the incident's `INCX#` items:

- A `reminder` for an incident that is no longer open is closed as `cancelled` (`incident_resolved`), and its
  pointer removed.
- A `reminder` or `resolved` notification whose incident's `opened` notification is not `delivered`, `failed`,
  or `cancelled` is skipped; its pointer stays and is retried on a later poll.

After the send, one transaction conditioned on `lease.token`: completes the attempt; sets the notification to
`delivered`, `retry_wait` (with `nextAttemptAt` from the retry schedule), or `failed`; deletes the old due
pointer; puts the new due pointer (retry) or the `ATTENTION#` pointer (failed).

The result classification is in [incidents and notifications](../architecture/incidents-and-notifications.md).
`outcome_unknown` and `process_stopped` are retried like transient failures; because the key and
`Idempotency-Key` are stable, a duplicate at the receiver is detectable.

### D8. Manual retry (C7)

`POST …/retry` on a `failed` notification: condition `state = "failed"`; sets `state = pending`, `nextAttemptAt =
now`, a flag `manualRetry = true`; deletes the `ATTENTION#` pointer and puts a due pointer. A manual retry makes
exactly one attempt; if it does not deliver, the notification returns to `failed` with a new `ATTENTION#` pointer.

### D9. Retention

Incidents, events, notifications, and attempts keep their evidence snapshots so they remain interpretable
after the 90-day observation expiry. Their own lifetime is decided in [ADR 0007 D1](ADR-0007-retention-and-release.md).

### D10. Package boundaries

- `internal/incident` (stdlib only): `Evaluate`, reminder slot math, payload construction, retry schedule,
  delivery-result classification vocabulary.
- `internal/notify`: delivery worker and HTTP sender with the dial-time destination policy; depends on
  interfaces and an injected clock.
- `internal/store`: the transactions above; typed errors; no SDK leakage.
- `internal/scheduler`: reminder step inside the existing tick.
- `internal/notifyreceiver` + `cmd/notification-receiver`: fixture only; never imported by the product.

### D11. Maintenance windows

| Entity | `PK` | `SK` |
| --- | --- | --- |
| Maintenance window | `MON#<monitorId>` | `MAINT#<startAt>#<windowId>` |

```
MaintenanceWindow
  entityType "maintenance", windowId, monitorId, startAt, endAt, note?, createdAt
  cancelledAt?     set when cancelled; an active window is cancelled by setting endAt = now

Monitor additions
  maintenance      map {
    windows          list of non-ended windows {windowId, startAt, endAt}, at most 10, sorted by startAt
    activeId?        the window the scheduler last recorded as active
  }
```

The monitor item carries the non-ended windows so the counted-result transaction (D3) can classify evidence
from the item it already conditions on. Every change to `maintenance` increments `evaluation.revision`.

- **Create:** put the window (`attribute_not_exists`) and append to `maintenance.windows` with a condition that
  the list still equals what was read (overlap was validated against it). Windows must not overlap, start no
  earlier than 5 minutes ago, and last at most 7 days.
- **Cancel:** a future window is marked `cancelledAt` and removed from the list; an active window gets
  `endAt = now` and `cancelledAt = now`. Past windows are read-only history.
- **Labelling:** decided in the lease-acquiring write (scheduled or manual) from `maintenance.windows` and the
  check's `startedAt`, carried with the work to the result as `maintenanceWindowId`, so a result recorded after
  the end boundary keeps its label. `incident.Evaluate` treats such evidence like `checker_problem`: runs
  unchanged, no transition.
- **Boundaries:** the scheduler tick compares `activeId` with the windows at `now`. On start: set `activeId`,
  clear runs, and add a `maintenance_started` event to an open incident. On end: remove `activeId`, drop the
  window from the list, clear runs, add `maintenance_ended`. Both are conditional on `maintenance.activeId`, so
  a boundary is recorded once. A boundary is recorded within one tick; while the scheduler is down, evidence is
  still labelled by time, so it is excluded correctly.
- **Notifications:** while a window is active the reminder step (D6) moves `nextReminderAt` to the first slot
  after now without sending. `opened` and reminders cannot occur during a window; `resolved` can only come from
  archive.

Rejected: recurring windows (cron-like rules need time-zone and DST semantics; not needed yet); suppressing
checks during windows (loses evidence that coverage summaries should report).

## Alternatives considered

- **Evaluating incidents asynchronously from the observation stream.** A second consumer would need its own
  cursor and ordering guarantees; joining the counted transaction reuses the lease ordering for free.
- **Incidents in a global `INCIDENTS` partition.** Easy cross-monitor listing, but per-monitor lists would need
  filtering or reference items; open incidents are already visible on the monitor list.
- **Notification state on the incident item.** Would make every delivery attempt contend with evaluation writes
  on the same item.
- **Sending from the check workers.** A slow receiver would delay checks; a separate worker isolates it.
- **Queue-free polling of all notifications.** Cost would grow with history; the `DELIVERY` pointer partition
  holds only actionable work.

## Consequences

- Monitor and observation writes gain incident-related updates; ADR 0003 D3–D6 transactions grow by the
  items listed in D3–D5.
- The web reads incident state from the API and never recomputes thresholds.
- The cloud path ([ADR 0008 D5](ADR-0008-cloud-path.md)) has no delivery worker and records intents as
  cancelled with reason `no_channel` instead of leaving them pending.
