# Local setup

Everything runs on this machine against loopback addresses. No AWS account, credentials, or paid service is
needed; the application refuses hosted endpoints by design. Dependency downloads (Go modules, npm packages, the DynamoDB Local image) need internet
access once.

## Prerequisites

| Tool | Version | Notes |
| --- | --- | --- |
| Go | 1.27.1 (`backend/go.mod`) | Install from https://go.dev/dl/ and put `go` on `PATH`. |
| Node.js | 24.x (`.nvmrc`) | Node 24 with npm 11; `make infra-synth` also needs `unshare` (util-linux) with unprivileged user namespaces. |
| Docker + Compose v2 | any current | Runs DynamoDB Local only. |
| GNU Make, git, curl | any | |

`make doctor` prints what is found and exits non-zero on a missing requirement.

## First run

```sh
make setup      # copies .env.example to .env, downloads Go modules, npm ci in web/ and infra/
make db-up      # pulls amazon/dynamodb-local:3.3.1 on first use; waits for healthy
make verify     # backend, web and infra checks (see docs/development/testing.md)
```

For the production-shaped local release:

```sh
make build  # web/dist plus backend/bin/statusforge, with git-describe version
make run    # rebuilds, starts/waits for DynamoDB Local; UI and API at http://127.0.0.1:8080/
```

Stop the foreground binary with Ctrl-C; the database volume remains. In development, instead
run `make backend-dev` and `make web-dev` in separate terminals. A plain Go build does not
embed a web build: open the Vite server, or run `make build` for a single-binary UI.
Optional local fixtures remain `make sample-target`, `make notification-receiver`, and
`make sample-job` after configuring its report URL and token.

**First monitor.**
1. Start `make run`, then run `make sample-target` in a second terminal.
2. In the UI, open **Monitors** → **Create a monitor**.
3. Create an HTTP monitor with the URL `http://127.0.0.1:8090/healthy`.

The URL uses the target's address from `STATUSFORGE_SAMPLE_TARGET_ADDR`, and that address must be listed in
`STATUSFORGE_ALLOWED_TARGETS`. The first check runs at the next interval slot. The API equivalent is:

```sh
curl -X POST http://127.0.0.1:8080/api/monitors -H 'Content-Type: application/json' \
  -d '{"name":"Local health","intervalSeconds":60,"check":{"url":"http://127.0.0.1:8090/healthy"}}'
```

Replace `8080` and `8090` in these examples, and in the runbook's, if you changed the addresses in `.env`.

**Shared local data.** Every checkout on one machine uses the same Compose project (`statusforge`,
pinned in `compose.yaml`), so they share one DynamoDB Local container and data volume. By default they
also share the table `statusforge`. To keep a second checkout's data separate, set a different
`STATUSFORGE_DYNAMODB_TABLE` in its `.env` before `make run`. Leave `STATUSFORGE_DYNAMODB_PORT` unchanged:
a different value would make Compose recreate the shared container.

## Configuration

`.env` (git-ignored) is loaded by `make run`, `make demo`, and the `make *-dev` targets.
Every variable is documented in [`.env.example`](../../.env.example). The backend reads
only `STATUSFORGE_*` names.

| Variable | Default | Rule |
| --- | --- | --- |
| `STATUSFORGE_HTTP_ADDR` | `127.0.0.1:8080` | Host must be loopback (`127.0.0.0/8`, `::1`, `localhost`). `:8080`, `0.0.0.0`, LAN IPs are rejected before listening. |
| `STATUSFORGE_DYNAMODB_ENDPOINT` | none — required | `http(s)://` URL with a loopback host. Unset or a hosted endpoint is rejected; there is no fallback. |
| `STATUSFORGE_DYNAMODB_REGION` | `local` | Any string; DynamoDB Local ignores it with `-sharedDb`. |
| `STATUSFORGE_DYNAMODB_ACCESS_KEY_ID` / `..._SECRET_ACCESS_KEY` | `local` | Placeholders the local server requires; not AWS credentials. |
| `STATUSFORGE_DYNAMODB_TABLE` | `statusforge` | 3–255 characters `[A-Za-z0-9_.-]`; created in DynamoDB Local on startup. |
| `STATUSFORGE_LOG_LEVEL` | `info` | `debug` `info` `warn` `error` |
| `STATUSFORGE_LOG_FORMAT` | `text` | `text` or `json` |
| `STATUSFORGE_SHUTDOWN_TIMEOUT` | `10s` | Go duration |
| `STATUSFORGE_READINESS_TIMEOUT` | `2s` | Bound on the DynamoDB readiness probe |
| `STATUSFORGE_SAMPLE_TARGET_ADDR` | `127.0.0.1:8090` | Loopback only |
| `STATUSFORGE_SAMPLE_TARGET_SLOW_DELAY` | `5s` | Max `60s` |
| `STATUSFORGE_ALLOWED_TARGETS` | `127.0.0.1:8090` | Non-empty comma-separated loopback `host:port` list (ports 1–65535); duplicate entries collapse. |
| `STATUSFORGE_WORKERS` | `4` | Scheduler worker pool size, integer 1–16. |
| `STATUSFORGE_MIN_INTERVAL_SECONDS` | `60` | One of `10`, `15`, `30`, `60`; values below 60 unlock heartbeat development intervals `10`, `15`, `30` s and grace `5`, `10` s. |
| `STATUSFORGE_SCHEDULER_ENABLED` | `true` | `true` or `false`; false serves the API with manual checks only (useful when observing staleness). |
| `STATUSFORGE_LIVENESS_INTERVAL_SECONDS` | `10` | Receive-liveness write interval in seconds, integer 2–60. An outage is detected after three intervals. |
| `STATUSFORGE_HOUSEKEEPING_INTERVAL_SECONDS` | `60` | Retention and deletion worker interval, integer 2–3600; independent of the scheduler toggle. |
| `STATUSFORGE_RECEIVER_ADDR` | `127.0.0.1:8091` | Loopback-only receiver fixture listen address. |
| `STATUSFORGE_NOTIFY_URL` | `http://127.0.0.1:8091/notify` | Required HTTP URL with loopback host, no userinfo or fragment; host and port alone form delivery's dial-time allow-list. |
| `STATUSFORGE_DELIVERY_WORKERS` | `1` | Fixed delivery pool, integer 1–4. |
| `STATUSFORGE_DELIVERY_RETRY_SCHEDULE` | `10s,30s,90s,5m,15m,30m` | 1–10 comma-separated Go durations, each 1 s–1 h, before subsequent attempts. |
| `STATUSFORGE_REMINDER_INTERVAL_SECONDS` | `21600` | Reminder slot interval in seconds, integer 60–86400. |
| `STATUSFORGE_SAMPLE_JOB_ADDR` | `127.0.0.1:8092` | Loopback-only sample-job fixture listen address. |
| `STATUSFORGE_SAMPLE_JOB_REPORT_URL` | empty | Loopback-only `http://127.0.0.1:8080/ingest/heartbeats/<id>`; required to report. |
| `STATUSFORGE_SAMPLE_JOB_TOKEN` | empty | Secret heartbeat token; set in local `.env` only, never commit. |
| `STATUSFORGE_SAMPLE_JOB_INTERVAL_SECONDS` | `60` | Sample-job interval, integer 1–86400. |
| `STATUSFORGE_SAMPLE_JOB_DEPLOY_URL` / `..._DEPLOY_TOKEN` | empty | Loopback-only application deployment ingest URL and its secret token; both required for `/control/deploy`. |
| `STATUSFORGE_DYNAMODB_PORT` | `8000` | Host port Compose publishes on `127.0.0.1` |
| `STATUSFORGE_WEB_PORT` | `5173` | Vite port (strict) |
| `STATUSFORGE_API_PROXY_TARGET` | `http://127.0.0.1:8080` | Vite `/api` proxy |

Changing `STATUSFORGE_DYNAMODB_PORT` requires matching `STATUSFORGE_DYNAMODB_ENDPOINT`.

## Ports

| Port | Service | Binding |
| --- | --- | --- |
| 8080 | Embedded web and Go API (`make run`) or API only (`make backend-dev`) | 127.0.0.1 |
| 8000 | DynamoDB Local | 127.0.0.1 (Compose port mapping) |
| 5173 | Vite dev server | 127.0.0.1 |
| 8090 | Sample target fixture | 127.0.0.1 |
| 8091 | Notification receiver fixture | 127.0.0.1 |
| 8092 | Sample job fixture | 127.0.0.1 |

The receiver defaults to `accept`. Change its mode with:

```sh
curl -X PUT http://127.0.0.1:8091/control/mode -H 'Content-Type: application/json' -d '{"mode":"drop"}'
curl http://127.0.0.1:8091/received
curl -X DELETE http://127.0.0.1:8091/received
```

Modes are `accept`, `fail`, `reject`, `timeout`, and `drop`. `timeout` reads the request then waits 10 s;
`drop` reads and stores it, then closes without responding. `accept`, `timeout`, and `drop` store received
entries; `fail` and `reject` do not. The receiver is unauthenticated, loopback-only, and memory-only
(restart clears its list).

Create a heartbeat monitor and copy its one-time token and `ingestPath`. Set
`STATUSFORGE_SAMPLE_JOB_REPORT_URL=http://127.0.0.1:8080/ingest/heartbeats/<id>` and
`STATUSFORGE_SAMPLE_JOB_TOKEN=<token>` in local `.env` (never commit tokens), then run `make sample-job`.
You can report once without the fixture:

```sh
curl -fsS -X POST -H 'Authorization: Bearer <token>' \
  'http://127.0.0.1:8080/ingest/heartbeats/<id>'
```

`GET http://127.0.0.1:8092/status` shows attempts without credentials. The sample job's
`PUT /control/mode` accepts `{\"mode\":\"normal|skip|fail|late|stop\",\"lateSeconds\":5}` (one mode at
a time); `POST /control/run` reports once with the current mode (`fail` reports failure;
`stop` returns 409), and `POST /control/replay` resends the previous report to exercise duplicate
handling. Control routes are unauthenticated and must remain loopback-only. A heartbeat proves
that a request carrying the token reached StatusForge at the recorded time, not that the job
did its work correctly or that an output such as a backup is usable. No report by the
deadline means none was received while StatusForge was receiving, not that the job did not run.
Receive outages shorter than three liveness intervals are not detected; after an outage, a job
that has really stopped is reported one full interval after StatusForge is receiving again.

Create an application on the Applications page or with `POST /api/applications` and save its
one-time `issuedToken`. Add an HTTP monitor and a heartbeat to the application using the
application selector on each monitor detail page. Deployment markers are context only: they
do not affect monitor health or prove that a deployment caused an incident. For example:

```sh
curl -X POST 'http://127.0.0.1:8080/api/applications' \
  -H 'Content-Type: application/json' -d '{"name":"Local service"}'
curl -X POST 'http://127.0.0.1:8080/ingest/applications/<application-id>/deployments' \
  -H 'Authorization: Bearer <application-token>' -H 'Content-Type: application/json' \
  -d '{"version":"local-1","deploymentId":"local-deploy-1"}'
```

To use the sample job instead, set `STATUSFORGE_SAMPLE_JOB_DEPLOY_URL` to the loopback
`http://127.0.0.1:8080/ingest/applications/<application-id>/deployments` and
`STATUSFORGE_SAMPLE_JOB_DEPLOY_TOKEN` to that application's token in local `.env`, restart
`make sample-job`, then:

```sh
curl -X POST 'http://127.0.0.1:8092/control/deploy' \
  -H 'Content-Type: application/json' \
  -d '{"version":"local-1","deploymentId":"local-deploy-1"}'
```

Schedule a one-minute maintenance window for an existing monitor (replace `<monitor-id>`):

```sh
start=$(date -u +%Y-%m-%dT%H:%M:%SZ)
end=$(date -u -d '+1 minute' +%Y-%m-%dT%H:%M:%SZ)
curl -X POST "http://127.0.0.1:8080/api/monitors/<monitor-id>/maintenance" \
  -H 'Content-Type: application/json' \
  -d "{\"startAt\":\"$start\",\"endAt\":\"$end\",\"note\":\"local maintenance\"}"
curl "http://127.0.0.1:8080/api/monitors/<monitor-id>/maintenance"
```

## Overview and monitor summaries

Open `/` for the Overview: receive outages, open incidents, failures before the incident
threshold, stale or unknown coverage, notification delivery problems, and recent recoveries
are separate sections. Its recovery window is 24 hours; the response limits open incidents
to 50, recoveries to 20, and displayed failed notifications to 10. The monitor list is at
`/monitors`.

An HTTP monitor's detail page offers 24-hour and 7-day summaries. **Expected checks** are
scheduled slots recorded as observations plus slots explicitly recorded as not observed
in scheduler gaps; manual checks are separate. Maintenance and not-counted checks are
excluded from outcome totals. Paused time is displayed separately and creates no slots.
Response-time figures include only checks receiving a response outside maintenance;
fewer than five responses have no median or p95. A stale monitor can show pending time
that the scheduler has not yet accounted for. Raw-read limits are 20,000 observations
and 5,000 gaps: a partial summary states the earliest covered instant. Pause history
before lifecycle recording began is unknown, not reconstructed.

Heartbeat summaries use deadlines as slots: a counted report (on time or late)
or an explicit missed deadline is recorded; a receive-outage or overdue gap
contributes not-observed slots. Failure reports are a subset of on-time and
late reports. Paused and older-than-current reports remain visible as
not-counted evidence but create no deadline slots. Maintenance-labelled
deadlines are separate from outcome totals, and heartbeat latency is null.
Archived monitors retain their history; ingest while archived is rejected.

The monitor timeline supports server-side filters (`outcome`, `counted`,
`maintenance`, `from`, `to`) and a **Load older** cursor. `outcome` is a
comma-separated subset of `healthy,failing,checker_problem`; heartbeat misses
have outcome `failing`. Times are RFC 3339. Observation and gap responses
include `nextCursor` (null when exhausted) and `searchedThrough` (oldest
examined instant); a sparse filter may return fewer matches than requested
while still having a next page. Cursors are opaque, monitor-specific, and
stable when newer evidence arrives. Each request examines at most 2,000
records, so use the next cursor to continue a long search.

## Troubleshooting

- **`go: command not found`** — put the Go installation's `bin` directory on `PATH`; `make doctor` reports what it finds.
- **API exits immediately with a configuration error** — the message names the variable; the guards are
  intentional (see [system overview](../architecture/system-overview.md#security-boundaries-in-force)).
- **`/api/health/ready` returns 503** — `docker compose ps`; run `make db-up`. The API does not need a restart.
- **DynamoDB container unhealthy after a volume change** — `docker compose up -d --force-recreate dynamodb-init dynamodb`.
- **Vite fails to start** — port 5173 busy (`strictPort`); change `STATUSFORGE_WEB_PORT`.
- **`421 host_not_allowed` / `403 origin_not_allowed`** — call the loopback host, not a
  foreign Host or Origin; do not expose the API to the LAN.
- **`415 unsupported_media_type` / `413 too_large`** — send `application/json` for a
  nonempty mutation body; maximum size is 64 KiB. Ingest retains its token and 4 KiB bound.
- **Plain Go server returns 503 for UI** — use `make build && make run` or open Vite from
  `make web-dev`. API and health routes still operate.
- **Resetting local data** — `make db-reset CONFIRM=yes` deletes the volume; see the
  [runbook](../operations/local-runbook.md).

## What is not set up

No container images for the applications and no AWS deployment tooling: the local product is a single
binary plus DynamoDB Local, and the cloud path is [synthesised, not deployed](../architecture/cloud-path.md).
CI runs the same `make` checks plus a DynamoDB Local integration job (`.github/workflows/ci.yml`).
