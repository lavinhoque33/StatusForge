# ADR 0002: Persistence — one table, monitors and observations

- Status: accepted (2026-09-27)
- Extends: [ADR 0001](ADR-0001-foundation-stack.md) (loopback-only DynamoDB endpoint, explicit client, no
  default credential chain)
- Later records build on this one: [ADR 0003](ADR-0003-scheduling-persistence.md) supersedes D7 (single
  flight becomes a durable lease) and D6's "no denormalised summary"; [ADR 0007](ADR-0007-retention-and-release.md)
  supersedes D8 (TTL is now enabled and every record type has a stated lifetime).

## Context

The first slice that needs data decides the table and key design. The design must start from product
queries, preserve lineage (what was checked, under which configuration version, when), state retention, and
guarantee that an old observation can never overwrite newer state. DynamoDB Local is the only database, but
nothing here may need a redesign to run against hosted DynamoDB.

### Access patterns

| # | Operation | Consistency need | Frequency |
| --- | --- | --- | --- |
| A1 | List monitors with lifecycle state (active, paused, archived) | Read-after-write: a monitor just created must appear | Every list page load |
| A2 | Get one monitor with its current configuration and version | Read-after-write | Every detail page load, every manual check |
| A3 | Create a monitor | Must not collide | Rare |
| A4 | Edit a monitor: check settings (bumps version) or name only | Must reject a concurrent edit | Rare |
| A5 | Change lifecycle: active ⇄ paused → archived | Must reject an invalid transition | Rare |
| A6 | Append an observation for a monitor | Duplicate-safe; never lost once reported to the user | Once per manual check |
| A7 | List a monitor's recent observations, newest first, bounded | Read-after-write: the check just run must appear | Every detail page load |

Reserved, not built here: pending work items and acquisition, a current-state summary with freshness
(ADR 0003), incidents and delivery attempts (ADR 0004), heartbeats and deployment markers (ADR 0005).

## Decisions

### D1. One table, generic keys, `entityType` discriminator

Table name from `STATUSFORGE_DYNAMODB_TABLE` (default `statusforge`). Key schema `PK` (string, hash) and
`SK` (string, range). No global secondary index. Billing mode `PAY_PER_REQUEST` in the create request
(DynamoDB Local ignores it). Every item carries `entityType`.

Rationale: one table means one create call, one variable, one IaC resource later. Prefixed keys give every
future entity (work, incident, heartbeat) a place without a migration.

### D2. Items and keys

| Entity | `PK` | `SK` | Notes |
| --- | --- | --- | --- |
| Monitor | `MONITORS` | `MON#<monitorId>` | One partition for the whole monitor list; see D4 for why this is acceptable. |
| Observation | `MON#<monitorId>` | `OBS#<startedAt>#<observationId>` | `startedAt` is UTC in the fixed-width layout `2006-01-02T15:04:05.000000000Z` so lexical order equals time order; `observationId` breaks ties. |

Reserved prefixes in the `MON#<monitorId>` partition: `WORK#`, `INC#`. Reserving names is not building them.

IDs are `crypto/rand.Text()` values (26 base32 characters, stdlib, no new dependency). They are opaque: no
timestamp is embedded, so ordering always comes from an explicit attribute.

### D3. Monitor item attributes

```
entityType      "monitor"
monitorId       string
name            string            1–100 characters after trimming
lifecycle       "active" | "paused" | "archived"
configVersion   number            starts at 1; +1 on every change to a `check.*` attribute
check           map {
  url             string          http:// URL whose host:port is in the allowed-target list at save time
  method          "GET"
  expectedStatus  number          100–599, default 200
  deadlineMs      number          1000–30000, default 10000
  maxBodyBytes    number          65536 (fixed, stored so history explains itself)
}
createdAt       string RFC3339Nano UTC
updatedAt       string RFC3339Nano UTC
pausedAt        string, present only while paused
archivedAt      string, present only when archived
```

Validation happens before any write: invalid targets are rejected per field and never stored. The store
trusts its inputs; the monitor domain package owns the rules.

### D4. Listing monitors from one partition (A1)

`Query PK = "MONITORS"` with `ConsistentRead: true`. Lifecycle filtering is done by the caller (the whole
list is a handful of items). A hot-partition concern would only apply in hosted DynamoDB at item counts this
product does not target; if that ever changes, a sparse GSI keyed by lifecycle is the migration, and no key
changes are needed for observations.

Rejected: a sparse GSI now (eventually consistent in the cloud, an extra resource, nothing to gain locally);
`Scan` with a filter (unbounded by design; forbidden as a habit).

### D5. Conditional writes replace revision counters (A3–A5)

| Operation | Condition expression |
| --- | --- |
| Create | `attribute_not_exists(PK)` |
| Edit check settings | `configVersion = :expected AND lifecycle <> "archived"`; update sets `check`, `configVersion = :expected + 1`, `updatedAt` |
| Edit name only | `configVersion = :expected AND lifecycle <> "archived"`; `configVersion` unchanged (a stale editor gets `version_conflict` even for a rename) |
| Pause / resume | `lifecycle = :from` (`active` ⇄ `paused`) |
| Archive | `lifecycle <> "archived"`; sets `archivedAt`; there is no reverse transition |

A failed condition maps to `409 Conflict` with a reason (`version_conflict`, `invalid_transition`,
`archived`). The web reloads the monitor and shows the current state instead of retrying blindly: no
uncertain duplicate actions.

### D6. Observation item is the only record of a check (A6, A7)

```
entityType      "observation"
monitorId       string
observationId   string
configVersion   number            copied from the monitor at check start
request         map               copy of `check` as executed (url, method, expectedStatus, deadlineMs, maxBodyBytes)
initiatedBy     "manual" | "scheduled"
startedAt       string            fixed-width UTC (same value as in SK)
completedAt     string            fixed-width UTC
durationMs      number
outcome         "healthy" | "failing" | "checker_problem"
reason          "ok" | "wrong_status" | "timeout" | "connection_refused" | "connection_error"
                | "refused_by_policy" | "internal"
observedStatus  number, absent when no response arrived
bodyBytesRead   number
bodyTruncated   boolean           true when the body exceeded maxBodyBytes and was cut
expiresAt       number            Unix seconds, startedAt + 90 days (D8)
```

Write: `PutItem` with `attribute_not_exists(PK)` (the full key; a repeated write of the same observation is a
no-op failure, never a second row). The observation is written **after** the check completes; a crash before
the write leaves no row and the monitor shows "unknown — no checks yet" or its previous result, which is the
intended presentation of an interrupted check.

`unknown` is deliberately **not** an outcome value: it is the absence of an eligible observation and is
computed by the reader. Storing it would let a row claim knowledge it does not have.

Read (A7): `Query PK = "MON#<id>" AND begins_with(SK, "OBS#")`, `ScanIndexForward: false`,
`ConsistentRead: true`, `Limit` = page size (default 50, maximum 200).

Lineage: `configVersion` plus the `request` snapshot make every row self-explaining even after the monitor
is edited, without storing configuration history items.

### D7. Single flight per monitor (superseded)

Originally a `POST …/checks` acquired a per-monitor in-memory lock; a second concurrent request received `409`
with reason `check_in_progress`. The lock was released when the check goroutine finished, including on client
disconnect: the check runs to completion under its own deadline so the observation is still recorded; the
HTTP request context is not the check context. ADR 0003 D4 replaced the in-memory lock with the durable
monitor lease; the `409 check_in_progress` contract and the run-to-completion rule are unchanged.

### D8. Retention is declared on every observation

Every observation carries `expiresAt` = `startedAt` + 90 days. Readers drop rows whose `expiresAt` has
passed, so an API result never depends on when physical deletion happens. ADR 0007 enables DynamoDB TTL and
extends the rule to every record type.

### D9. Table creation at startup, readiness unchanged

`cmd/statusforge` calls `DescribeTable`; on `ResourceNotFoundException` it calls `CreateTable` and waits
for `ACTIVE` (bounded by `STATUSFORGE_READINESS_TIMEOUT` × 5). This runs only through the ADR 0001 client,
so it can only ever touch the configured loopback endpoint. If the database is unavailable at startup, the
application still starts degraded and retries table creation on the next request that needs the store;
`/api/health/ready` keeps using `ListTables`.

Rejected: a separate migration command (a second thing to remember before first run; nothing to migrate).
A later schema change introduces its own migration path ([ADR 0007 D6](ADR-0007-retention-and-release.md)).

### D10. Store package boundary

`internal/store` is the only product package besides `localdynamo` that imports the AWS SDK, and it
exposes domain types, not SDK types. Domain rules (validation, lifecycle transitions, outcome
classification) live outside it so other adapters can reuse them. The store maps
`ConditionalCheckFailedException` to typed errors; no SDK error reaches an HTTP handler or a log line with a
URL or credential in it.

## Alternatives considered

- **Two tables (`monitors`, `observations`).** Clearer per-table schema; rejected because it doubles
  creation, configuration, and future IaC for no query benefit.
- **Monitor `META` item inside the `MON#<id>` partition.** One partition per monitor is elegant, but listing
  monitors would then need a GSI or a scan. Keeping monitors in `MONITORS` gives a consistent list query for free.
- **Denormalised last-observation summary on the monitor item now.** The right design once a scheduler
  exists; rejected here because it needs an eligibility rule that only makes sense with concurrent writers
  (ADR 0003 D5 adds it).
- **Storing `unknown` observations.** Rejected; see D6.
- **Configuration history items.** Rejected; the request snapshot covers lineage.
- **UUID library.** Rejected; `crypto/rand.Text()` is stdlib and sufficient for opaque IDs.
- **Enabling DynamoDB TTL now.** Deferred until retention was decided for every record type (ADR 0007).

## Consequences

- Anything that reads monitor state computes `unknown`/stale from observations, never from a stored value.
- `make db-reset CONFIRM=yes` remains the only way to drop the table; a schema change needs a new ADR.
