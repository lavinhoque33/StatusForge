# HTTP API

The management API (`/api/…`) and the ingest routes (`/ingest/…`) are served by `cmd/statusforge` on
`127.0.0.1:8080` alongside the embedded web UI. JSON only; same-origin only (no CORS headers). Routes are
registered in `backend/internal/httpapi/monitors.go`; handlers are split by subsystem in the same package.

## Conventions

- Times are RFC 3339 UTC with millisecond precision. IDs are opaque strings.
- Every `/api` response sets `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`.
- Errors are `{"error": "<code>"}`; validation failures are
  `400 {"error":"validation_failed","fields":{"check.url":{"code":"target_not_allowed","message":"…"}}}`.
  Field keys are JSON paths; codes are the contract, messages may change. Unknown JSON fields are rejected
  (`unknown_field`); malformed JSON is `400 invalid_json`.
- Store failures are `503 store_unavailable` on every route, logged with a coarse reason and never with the
  endpoint URL.
- Unknown `/api` or `/ingest` paths are `404 not_found`; wrong methods `405 method_not_allowed`. Any other
  path serves the web app (history fallback).
- Every mutation of an item under permanent deletion is `409 deleting` (`deletionGuard` middleware).

### Protection (every route; `protection.go`)

| Rule | Response |
| --- | --- |
| `Host` must be `127.0.0.1`, `localhost`, or `[::1]` (any port) — DNS-rebinding defence | `421 host_not_allowed` |
| State-changing `/api` request with a non-loopback `Origin`, a literal `null` Origin, or `Sec-Fetch-Site: cross-site` | `403 origin_not_allowed` |
| State-changing `/api` body over 64 KiB (unknown-length bodies are bounded the same way) | `413 too_large` |
| Non-empty body without `Content-Type: application/json` | `415 unsupported_media_type` |
| Ingest body over 4 KiB | `413 too_large` |

The served page carries `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self';
img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; object-src 'none';
base-uri 'none'` (`internal/webui/webui.go`) with no inline script. The management API is unauthenticated
because it is loopback-only; a shared or hosted interface would need an authentication design first.

## Health and system

| Route | Response |
| --- | --- |
| `GET /api/health/live` | `200 {"status":"ok"}` — the process is serving |
| `GET /api/health/ready` | `200 {"status":"ready","checkedAt","dependencies":{"dynamodb":{"status":"ready"}}}` or `503 {"status":"degraded",…,"reason":"timeout"|"unreachable"|"error"}` from a bounded `ListTables` |
| `GET /api/system` | version, table, `dataFormat`, `upgradedFrom`, backfill state, TTL status, the retention table, housekeeping counters, configured limits, `demo` flag. Never credentials or token material |
| `GET /api/system/liveness` | `{"aliveAt","outages":[{from,to}…]}` newest first |
| `GET /api/system/scheduler` | `{"state":"ok"|"behind"|"unknown"|"disabled","windowMinutes","dueChecks","missedChecks","workers"}`; no table access |
| `GET /api/intervals` | Allowed HTTP intervals and defaults for this process, plus `heartbeat {intervalSeconds[], graceSeconds[], defaults}`; no store access |

## Monitors

| Route | Request | Success | Errors |
| --- | --- | --- | --- |
| `GET /api/monitors` | — | `{"monitors":[Monitor…]}`, all lifecycles, `createdAt` ascending | 503 |
| `POST /api/monitors` | HTTP: `{"name","check":{"url","expectedStatus"?,"deadlineMs"?},"intervalSeconds"?,"incidentPolicy"?}`; heartbeat: `{"kind":"heartbeat","name","heartbeat":{"intervalSeconds","graceSeconds"},"incidentPolicy"?}` | `201 Monitor` (+ `issuedToken` for heartbeats; shown once); `Location` header; a `create` run is queued | 400, 413, 503 |
| `GET /api/monitors/{id}` | — | `Monitor` | 404 `monitor_not_found` |
| `PATCH /api/monitors/{id}` | `{"expectedConfigVersion", "name"?, "check"?, "intervalSeconds"?, "incidentPolicy"?, "heartbeat"?}` (partial `check`) | `Monitor`; a check-setting change bumps `configVersion` and queues a `config_change` run | 400, 404, 409 `version_conflict` \| `archived` \| `deleting` |
| `POST /api/monitors/{id}/lifecycle` | `{"action":"pause"|"resume"|"archive"}` | `Monitor`; resume queues a `resume` run | 409 `invalid_transition` \| `archived` |
| `POST /api/monitors/{id}/checks` | empty | `201 Observation` after the check completes (runs under its own deadline, not the request context) | 409 `archived` \| `check_in_progress` \| `not_supported` (heartbeat) |
| `GET /api/monitors/{id}/observations` | `limit` 1–200 (50), `before`, `outcome`, `counted`, `maintenance`, `from`, `to` | `{"observations":[…],"nextCursor","searchedThrough"}` newest first | 400 |
| `GET /api/monitors/{id}/gaps` | `limit`, `before` | `{"gaps":[…],"nextCursor","searchedThrough"}` | 400 |
| `GET /api/monitors/{id}/summary` | `window=24h|7d` | summary ([summaries](summaries-and-overview.md)) | 400 |
| `GET /api/monitors/{id}/incidents` | `limit` | `{"incidents":[…]}` newest first | |
| `GET /api/monitors/{id}/incidents/{incidentId}` | — | `{"incident","events","gaps","notifications","nearbyDeployments"}` | 404 `incident_not_found` |
| `POST …/incidents/{incidentId}/notifications/{noteKey}/retry` | — | `202 Notification` (one manual attempt) | 409 `not_failed`, 404 `notification_not_found` |
| `GET /api/monitors/{id}/maintenance` | `limit` | `{"windows":[…]}` active and scheduled first | |
| `POST /api/monitors/{id}/maintenance` | `{"startAt","endAt","note"?}` | `201 Window` | 400 `overlaps` \| `too_many_windows`, 409 `archived` |
| `POST …/maintenance/{windowId}/cancel` | — | `Window` (scheduled → cancelled; active → ends now) | 409 `window_closed` |
| `POST /api/monitors/{id}/heartbeat/token` | — | `201 {"token","hint","createdAt"}` (issue or rotate) | 409 `archived` |
| `DELETE /api/monitors/{id}/heartbeat/token` | — | `204` (revoke) | |
| `PUT /api/monitors/{id}/application` | `{"applicationId": id | null}` | `Monitor` | 404, 409 `archived` |
| `GET /api/monitors/{id}/deletion` | — | deletion status | 404 |
| `POST /api/monitors/{id}/deletion` | `{"confirmName"}` | `202 {state, requestedAt, updatedAt, removedItems}` | 400 `mismatch`, 409 `not_archived` |

`Monitor` carries `kind`, `lifecycle`, `configVersion`, `check` or `heartbeat`, `intervalSeconds`,
`incidentPolicy`, `status` (evaluated on read: `{state, reason, observation, freshUntil, evaluatedAt}`),
`openIncident`, `maintenance {active, next}`, `expectation` (heartbeats), `applicationId`, `deletion`,
timestamps.

### Target policy for `check.url`

Validation order; the first failure is the field's code: `url_invalid` (not absolute, > 2048 bytes) →
`scheme_not_allowed` (only `http`) → `url_has_userinfo` → `url_has_fragment` → `host_not_loopback` →
`target_not_allowed` (`host:port` not in `STATUSFORGE_ALLOWED_TARGETS`). The same policy runs again at dial
time, so an allow-list change takes effect immediately.

## Incidents and notifications

| Route | Behaviour |
| --- | --- |
| `GET /api/incidents?state=open|resolved|all&limit=N` | Open first (newest `openedAt`), then resolved by `resolvedAt` |
| `GET /api/notifications/attention?limit=N` | Failed notifications newest first, each with `monitorId`, `monitorName`, `incidentId` |
| `GET /api/overview` | Composed sections ([summaries](summaries-and-overview.md#get-apioverview)) |

`noteKey` in paths is `opened`, `resolved`, or `reminder-<nnnn>`.

## Applications and deployments

| Route | Behaviour |
| --- | --- |
| `GET /api/applications` | All applications with `token {hint, createdAt} | null`, `members[{id,name,kind}]` derived from monitor items |
| `POST /api/applications` | `{"name"}` unique among non-archived → `201` + `issuedToken` |
| `GET` / `PATCH /api/applications/{id}` | Read; rename (`409 archived`) |
| `POST /api/applications/{id}/archive` | Archives; clears membership; revokes token; markers kept |
| `POST` / `DELETE /api/applications/{id}/token` | Issue or rotate; revoke |
| `GET /api/applications/{id}/deployments?limit=N` | Markers newest first |
| `POST /api/applications/{id}/deployments` | Manual marker (`source: manual`); `409 duplicate_deployment` |
| `GET` / `POST /api/applications/{id}/deletion` | As for monitors |

## Ingest (token-authenticated)

| Route | Auth | Success |
| --- | --- | --- |
| `POST /ingest/heartbeats/{id}` | `Authorization: Bearer sfh_…` | `202 {"accepted":true,"duplicate":false,"counted","notCountedReason","late","nextDueAt"}` or `200 {"accepted":true,"duplicate":true}` |
| `POST /ingest/applications/{id}/deployments` | `Bearer sfd_…` | `202 {"accepted":true,"duplicate":false,"marker"}` or `200 duplicate` |

Order of checks and codes: size (`413`) → authentication (`401 unauthorized`, one body for every cause) →
rate limit (`429 rate_limited` + `Retry-After`) → lifecycle (`410 archived`) → validation (`400`) → write
(`503`). Bodies are documented in [heartbeats and deployments](heartbeats-and-deployments.md).

## Logging

One access-log line per request: method, path, status, duration, request ID, and domain IDs where relevant.
Never bodies, headers, query strings, or token material. Per-check, per-attempt, per-tick, and coverage
transition lines are described with their subsystems.
