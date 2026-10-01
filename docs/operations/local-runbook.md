# Local runbook

Start, stop, inspect, restart, and recover the local system. All commands run from the repository root and
touch only loopback services on this machine. Nothing here contacts AWS.

## Start

```sh
make build         # Vite production build and versioned backend/bin/statusforge
make run           # rebuilds, starts DynamoDB Local on loopback; embedded UI and API on 127.0.0.1:8080
```

The foreground process stops with Ctrl-C; `make run` retains the database container and data.
`backend/bin/statusforge version` prints the version embedded by `make build`; the same value
appears in `/api/system`, Settings, and the page footer. For Vite development instead, run
`make backend-dev` and `make web-dev` in separate terminals; a plain Go build without the
embedded web responds 503 with instructions to run `make build`.

If DynamoDB Local is unavailable, the API still serves liveness (200) with degraded readiness (503)
and retries table initialization until the dependency returns. A reachable table with TTL configured
on another attribute or a newer data format fails startup before the API listens. If that
incompatibility appears during deferred initialization, the API logs it and exits non-zero.

## Inspect

```sh
curl -s http://127.0.0.1:8080/api/health/live
curl -s -i http://127.0.0.1:8080/api/health/ready     # 200 ready / 503 degraded with a reason class
curl -s http://127.0.0.1:8080/api/system/scheduler     # scheduler coverage: unknown / disabled / ok / behind
docker compose ps                                     # dynamodb should be "healthy"
docker compose logs --tail 50 dynamodb
```

Backend logs go to stderr as structured `slog` lines (`STATUSFORGE_LOG_FORMAT=json` for machine parsing).
Each request logs method, path, status, duration, and request ID only.

## Stop

- Application: Ctrl-C; the API shuts down gracefully within `STATUSFORGE_SHUTDOWN_TIMEOUT`
  (default 10s). `make run` leaves DynamoDB Local running and data intact.
- Database, if intentionally stopping it later: `make db-down` (retains data). Never reset
  a table to recover from a transient outage.

## Retention and TTL

The API enables DynamoDB TTL on `expiresAt` at startup and refuses tables configured with TTL on another
attribute. `GET /api/system` shows TTL state, the format marker, and pending housekeeping jobs. Expired rows
disappear from API views immediately; DynamoDB Local physically removes them later. Checks and gaps live 90
days; scheduling work and heartbeat run guards 7 days; lifecycle events, maintenance windows, and deployments
365 days. Resolved incidents and their notifications, attempts, events, and attention pointers remain 365 days
from resolution, but are not stamped until all notifications finish. Open incidents never expire. A failed
notification manually retried protects the incident while delivery is pending again.

Housekeeping runs independently of scheduling. It budgets up to two seconds per phase and retries
within one second while backfill or deletion jobs remain; the configured interval (default 60 seconds)
applies when idle. Stopping the process mid-job is safe: durable cursors and jobs resume after restart.

## Export and import

**Stop the API before export.** A scan of a running table is not a consistent snapshot; the CLI warns if its
liveness row is present. Exports contain token hashes (never raw tokens) and are private local data. Files
are created with mode `0600`, and existing paths are never overwritten.

```sh
make db-export TABLE=statusforge FILE=.local/exports/local-copy.jsonl
make db-import FILE=.local/exports/local-copy.jsonl TABLE=statusforge_restored
```

`make db-export` without `FILE` chooses a UTC timestamp under `.local/exports/`. Import validates the
entire header, item lines, count, and SHA-256 digest before writing, refuses non-empty targets, and enables TTL.
Choose a new table for restore; importing preserves stored timestamps, so already-expired rows can be removed
by TTL. Keep private copies outside shared directories and never commit them.

## Permanent deletion

**Export first.** Only archived monitors and applications can be permanently deleted. On their detail page,
type the exact current name to confirm. A monitor waits for any ongoing notification to finish, then the
worker removes its delivery pointers and records in bounded batches; an archived application loses its
markers and duplicate guards. Deletion progress survives API restart. The operation cannot be undone except
by importing an earlier export into a new empty table.

## Upgrade

Stop the running API, then **export before replacing the binary**:

```sh
make db-export TABLE=statusforge FILE=.local/exports/pre-upgrade.jsonl
make build
make run
curl -fsS http://127.0.0.1:8080/api/system
```

The new binary enables `expiresAt` TTL and records data format 6. For a table written by an earlier build (no format marker),
resumable housekeeping backfills expiration stamps and creates retention jobs for resolved
incidents; wait for `/api/system` `backfill.state` to say `done`. Do not downgrade to a binary
that does not understand the marker. A newer format or incompatible TTL attribute is a startup
error; use a matching binary, not a reset. Export is private and not a consistent snapshot
while the API writes.

## Durable local data

DynamoDB Local persists to the named volume `statusforge_dynamodb-data` (`shared-local-instance.db`).
`docker compose restart dynamodb` and `make down && make db-up` retain tables and items.

## Destructive reset (explicit)

```sh
make db-reset CONFIRM=yes   # removes containers AND the data volume
```

Without `CONFIRM=yes` the target refuses. Permanent deletion and TTL are the other deliberate data-removal paths.

## Browser and API protection

The service binds to loopback and accepts only `Host: 127.0.0.1`, `localhost`, or `[::1]`
(with any port). A foreign Host returns JSON `421 host_not_allowed`, even for the UI and
health routes. `POST`, `PUT`, `PATCH`, and `DELETE` under `/api` reject a foreign or opaque
`Origin` and `Sec-Fetch-Site: cross-site` with `403 origin_not_allowed`; curl with no Origin
and the local Vite proxy work. A nonempty body must have `Content-Type: application/json`
(`415 unsupported_media_type`); more than 64 KiB returns `413 too_large`. Ingest retains its
separate token and 4 KiB bound. The served UI uses a restrictive Content Security Policy;
assets are cached long-term and HTML is revalidated. This is not authentication: never
publish the API on a shared interface.

## Demo

```sh
make demo                  # seeds statusforge_demo, then runs the API and fixtures live
DEMO_RESET=yes make demo   # deletes and reseeds statusforge_demo only
```

`make demo` builds the release binary, seeds the dedicated table `statusforge_demo` through the store's own
write paths with a simulated clock, then runs the embedded UI and API with the sample target, notification
receiver, and sample heartbeat job on loopback. The scene covers the seven days before seeding, at 15-minute
check and heartbeat intervals:
- checks with healthy, failing, and slow periods and a coverage gap;
- open and resolved incidents with delivered and failed notifications;
- a maintenance window;
- a heartbeat with late and missed reports;
- an application with deployment markers.

Every page shows a **Demo data** banner. Seeding takes about two minutes on the reference workstation. The
Overview then shows **StatusForge was not receiving** for that span, because nothing was checked while the
scene was written.

It refuses any other table name, and refuses a populated demo table unless `DEMO_RESET=yes`. Ports default to
8080/8090/8091/8092; override them with `STATUSFORGE_HTTP_ADDR`, `STATUSFORGE_SAMPLE_TARGET_ADDR`,
`STATUSFORGE_RECEIVER_ADDR`, and `STATUSFORGE_SAMPLE_JOB_ADDR` (loopback only), for example
`make demo STATUSFORGE_HTTP_ADDR=127.0.0.1:8180`. Ctrl-C stops the API and all fixtures; the table remains.
Demo data is synthetic: it is not evidence of any service's reliability.

## Capacity

With DynamoDB Local running and ports 8200–8203 free:

```sh
cd backend && go run ./cmd/capacity -duration 10m -stages 25,100,250,restart,outage -out /tmp/capacity.json
```

The driver:
- builds the API and sample target;
- creates HTTP monitors at 10 s in separate `statusforge_capacity_*` scratch tables;
- simulates a database outage by cutting a loopback proxy (it never stops the container);
- prints JSON;
- deletes its tables.

The full run takes about 50 minutes. Results and limits are in the [capacity report](capacity-report.md). In
short, on the measured workstation this build sustained about 10 scheduled checks per second with no gaps:
100 monitors at 10 s, or 250 monitors at the default 60 s. At 250 monitors and 10 s it could not keep up: it
recorded the missed slots as gaps and showed **Scheduler is behind**.

## Security check

```sh
npm --prefix web ci     # once; the licence and audit checks read installed production packages
npm --prefix infra ci   # once; the same for the CDK app
make security-check
```

`make security-check` is separate from `make verify` and runs as its own CI job. It runs these checks:
- pinned `govulncheck` on the backend;
- `npm audit --omit=dev` at level low for `web/`, and for `infra/` with the version-pinned accepted risks in
  `infrastructure/scripts/security/audit-exceptions.json` (a new advisory, path, or version, or a stale entry,
  fails);
- a licence allowlist for production Go and npm dependencies, including `aws-cdk-lib`'s bundled packages;
- pinned `gitleaks` over the working tree and git history.

It needs Go, Node/npm, and Python 3, and contacts only the Go vulnerability database, the Go module proxy, and
the npm registry. Investigate every finding. Rotate any exposed secret; scanning cannot erase history. Record a
licence exception only for a known licence, with a reason, in
`infrastructure/scripts/security/licence-exceptions.json`. Controls and known gaps:
[security review](security-review.md).

## Cloud path: build and synthesis only

Nothing in this section deploys, bootstraps, or contacts AWS. There is no deploy target.

```sh
make lambda-build   # backend/bin/lambda/{planner,worker}/bootstrap (linux/arm64, git-ignored)
make infra-synth    # lambda-build, then CDK synth into infra/cdk.out and the template check
make infra-check    # Prettier, tsc, Vitest template assertions, then infra-synth (part of make verify)
```

- `make infra-synth` runs the pinned CDK CLI (`synth --lookups=false --no-notices --no-version-reporting`)
  inside `unshare -rn`: a user and network namespace with only loopback, so every network attempt fails. It
  passes only `PATH`, a throwaway `HOME` (removed afterwards, so `~/.aws`, `~/.cdk` and SSO caches are never
  read), `CDK_DISABLE_CLI_TELEMETRY=true` and `CDK_DISABLE_VERSION_CHECK=true`; no `AWS_*` variable is set.
  It then checks the CLI-produced template against the resource and IAM rules in
  [cloud path](../architecture/cloud-path.md) and that `infra/cdk.context.json` was not written.
- It needs `unshare` (util-linux) and unprivileged user namespaces. Without them the target fails; it never
  falls back to synthesis with network access. On Ubuntu 24.04 with
  `kernel.apparmor_restrict_unprivileged_userns=1` it fails with `Operation not permitted`; CI relaxes that
  setting on its ephemeral runner only.
- Run `npm --prefix infra ci` once first (`make setup` does it).
- The Lambda binaries read these variables, which the stack sets: `STATUSFORGE_TABLE` (both),
  `STATUSFORGE_WORK_QUEUE_URL` (planner), `STATUSFORGE_CLOUD_TARGETS` (both; empty refuses every check),
  `STATUSFORGE_CLOUD_MONITORS` (planner; `[]` by default) and `STATUSFORGE_LOG_LEVEL` (`info`). The committed
  defaults are closed: schedule `DISABLED`, no targets, no monitors. Run locally, either binary exits 1 with
  `not running in AWS Lambda`.

What a real deployment would still have to prove is listed in [cloud path](../architecture/cloud-path.md#what-only-real-aws-can-prove).

## Recovery scenarios

| Situation | What you see | Recovery |
| --- | --- | --- |
| DynamoDB stopped while the API runs | `/api/health/ready` → 503 `unreachable` (or `timeout`); warning in API log; web shows "Degraded" with `dynamodb: unavailable (unreachable)` | `make db-up`; next readiness check returns 200. No API restart needed. |
| API stopped | Browser cannot connect; Vite reports "Unreachable" in dev mode | `make run` for the release, or `make backend-dev` with Vite; click "Check again". |
| Port already in use | API exits 1 with a bind error; Vite exits (strictPort) | Free the port or change `STATUSFORGE_HTTP_ADDR` / `STATUSFORGE_WEB_PORT` in `.env` (loopback host only). |
| Misconfiguration | API exits 1 before listening with a message naming the variable | Fix `.env`; see [local setup](../development/local-setup.md#configuration). |
| Volume permission error in DynamoDB logs | Container unhealthy | `docker compose up -d --force-recreate dynamodb-init dynamodb`; the init step re-applies ownership. |
| UI responds 503 "Web UI not embedded" | Plain `go run`/`backend-dev` does not include the production UI | Use `make build && make run`, or use `make web-dev` alongside `make backend-dev`. |
| Host or Origin rejected | `421 host_not_allowed` or `403 origin_not_allowed` | Use a loopback URL for the API and same-origin UI; remove a foreign Host/Origin. Do not relax binding or expose it to the LAN. |
| Mutation refused | JSON `415 unsupported_media_type` or `413 too_large` | Send `Content-Type: application/json` for a nonempty JSON body and keep it at or below 64 KiB. |

## Sample target control

```sh
curl -s http://127.0.0.1:8090/control/mode
curl -s -X PUT http://127.0.0.1:8090/control/mode -H 'Content-Type: application/json' -d '{"mode":"failing"}'
curl -s -i http://127.0.0.1:8090/            # 500 while failing
curl -s -X PUT http://127.0.0.1:8090/control/mode -H 'Content-Type: application/json' -d '{"mode":"healthy"}'
```

Modes: `healthy`, `failing`, `slow` (delay from `STATUSFORGE_SAMPLE_TARGET_SLOW_DELAY`, default 5s, max 60s).
Fixed endpoints `/healthy`, `/failing`, `/slow` ignore the mode. The fixture has no authentication and is
bound to loopback; never publish it.

## Coverage gaps

When this machine sleeps or the API is stopped, no checks run. On the next start the scheduler records the
missed slots as a `not_scheduled` gap and runs one catch-up check per monitor; nothing backfills history.
Monitor detail, summaries, and Overview show gaps as missing coverage, never as healthy time.

## Scheduler is behind

The Overview shows **Scheduler is behind** when more than 5% of the checks due in the last 5 minutes were
missed, meaning they were not started before their next slot. Each missed slot is recorded as an `overdue` gap
("workers were busy"). The same state is on `GET /api/system/scheduler`. The log has WARN
`scheduler coverage degraded` when it starts and INFO `scheduler coverage recovered` when it ends. Readiness
stays `200`, because readiness only says the database answers.

1. Look at the `scheduler tick` log lines (`STATUSFORGE_LOG_LEVEL=info`). A growing `backlog` or non-zero
   `skipped_expired` means scheduled work exceeds capacity; `cycle_ms` is the time of one scheduling pass.
2. Lengthen check intervals or archive monitors first. On DynamoDB Local the store work per check is the usual
   limit (about 10 checks per second on the reference workstation).
3. Raise `STATUSFORGE_WORKERS` (up to 16) only when checks spend their time waiting on slow targets, for
   example responses near their deadline.

The state clears on its own about 5 minutes after the load fits again. It describes this process only:
- it reads `unknown` after a start until a due check resolves, and `disabled` when
  `STATUSFORGE_SCHEDULER_ENABLED=false`;
- time when StatusForge or the database was down shows as `not_scheduled` gaps, not as `behind`.

## Not covered

Hosted DynamoDB TTL timing and cloud behaviour are not verified by this local runbook.
