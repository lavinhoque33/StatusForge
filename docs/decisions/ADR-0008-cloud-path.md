# ADR 0008: Cloud path — planner and worker Lambdas, shared slot runner, hosted-store seam

- Status: accepted (2026-09-30)
- Extends: [ADR 0003](ADR-0003-scheduling-persistence.md) (work items, claims, gaps),
  [ADR 0004](ADR-0004-incidents-notifications.md) (notification intent),
  [ADR 0007](ADR-0007-retention-and-release.md) (retention, format marker). Supersedes nothing in the local
  product: the local binary, its configuration, and its loopback guards are unchanged.

## Context

The cloud execution path is **prepared but not deployed**. It needs explicit event adapters, infrastructure
definitions, local contract tests, a permission design, workload and retention bounds, a cleanup design, and a
list of behaviour that still needs real AWS. The path is EventBridge Scheduler → SQS → Go Lambda → DynamoDB.
Preparation is separated from execution: nothing in this repository may look up an account, create resources,
or reach a hosted endpoint.

The local product already holds the rules the cloud path needs:

- `Store.Claim` → `checker.Run` → `Store.RecordResult` is the check path. Incident evaluation and notification
  intent are written in the same result transaction.
- `Store.TickCycle` materialises slots, writes `not_scheduled` gaps, and closes overdue and lease-expired work.

Four things blocked reuse:

- `store.New` required `*localdynamo.Client`.
- `Store.Initialize` creates the table and enables TTL.
- There was no exact work lookup.
- `targetpolicy` is loopback-only.

Notification delivery is a local worker, so a cloud path would leave intents pending forever.

## Decisions

### D1. Producer: scheduler → planner Lambda → SQS → worker Lambda

- One EventBridge Scheduler schedule, `rate(1 minute)`, invokes the **planner**. Each invocation runs one bounded
  pass:
  1. reconcile the declared monitors (D8);
  2. run `TickCycle` (slots, gaps, expiry);
  3. run one bounded retention-jobs step (ADR 0007 D3);
  4. send one SQS message per **dispatchable** work item.
- Dispatchable means either:
  - `pending` with `dueAt ≤ now < dueAt + interval`; or
  - `claimed` with an expired lease and `attempts < 2`.
- The durable work row is written before the send. A failed send leaves the row dispatchable, so the next pass
  sends it again.
- Duplicate messages are expected and harmless (D2).
- The planner's correctness must not depend on warm-container memory. The in-memory caches in `Store`
  (`lastSuccessfulTick`, `needsRecovery`) are hints only, and a cold start must produce the same durable result.
- Scheduler precision is 60 s, so cloud monitor intervals are at least 60 s.

### D2. Message and acknowledgement (worker)

- Message body: `{"v":1,"monitorId":"<id>","dueAt":"<stamp>"}`. The durable row decides eligibility; the message
  is only a pointer.
- For each record, the worker strongly reads the work item through the exact `Store.GetWork(monitorID, dueAt)`
  and runs the shared runner (D3).
- Acknowledged outcomes (the message is removed):
  - `recorded` (counted or not counted);
  - `not_eligible` (done, cancelled, stale version, inactive monitor);
  - `lease_held` (another delivery holds the lease; if that delivery dies, the planner re-dispatches after lease
    expiry, per D1);
  - `expired` (closed as an `overdue` or `lease_expired` gap);
  - `missing` (no such work item).
- Reported in `batchItemFailures`, so SQS redelivers after the visibility timeout:
  - `dependency_failure` (a transient DynamoDB error or timeout);
  - `invalid_message` (bad JSON or unknown `v`).
- Repeated failures reach the dead-letter queue through the redrive policy, which keeps invalid messages as
  evidence instead of dropping them.
- A target failure is recorded evidence, never a processing failure: the worker never retries a message because
  the observed outcome was adverse.

### D3. Shared one-slot runner

Package `internal/checkwork`:

- `RunSlot(ctx, work, now)` performs claim → check under the monitor's deadline → record with the scheduled
  trigger and `dueAt`. It returns one classified outcome from D2.
- The local scheduler's worker calls it in place of its former inline code. Coverage recording, feed fairness,
  and logging stay in `scheduler`, driven by the outcome.
- The scheduling guarantees of ADR 0003 are unchanged.
- The package imports neither `aws-lambda-go` nor any SDK.

### D4. Hosted-store seam

- `store.New` takes the SDK's `*dynamodb.Client` instead of `*localdynamo.Client`. `cmd/statusforge` keeps
  building it only through `localdynamo.New`, which is unchanged.
- Initialisation splits in two:
  - **Local** `Initialize` is unchanged: it creates the table, enables TTL, and writes the marker.
  - **Hosted** `Validate`: the table must be `ACTIVE` with the `PK`/`SK` string key schema, and TTL must be
    `ENABLED` on `expiresAt`; otherwise it fails closed with a named error. It then writes or reads the format
    marker with item-level calls only. It never calls `CreateTable`, `UpdateTable`, `UpdateTimeToLive`, or
    `DeleteTable`; the infrastructure owns the table.
- Package `internal/hosteddynamo` builds the client from the SDK default configuration: Lambda environment
  credentials from the execution role, `AWS_REGION`, and a table name from `STATUSFORGE_TABLE`. It accepts no
  endpoint override. Only the Lambda binaries import it.

### D5. Notifications: "no channel" in the cloud

- A store option `Notifications` has two values:
  - `deliver` (the local default; behaviour unchanged);
  - `none`, used by the Lambda binaries.
- With `none`, `evaluationItems` writes each intent (opened, resolved) as `state: "cancelled"` and
  `cancelledReason: "no_channel"`, and puts **no** `DELIVERY` due pointer. The incident events are unchanged.
- Existing rules already treat `cancelled` as final, so the incident retention job (ADR 0007 D3) stamps resolved
  incidents normally.
- The incident summary counts neither `delivered` nor `pending` for these.
- Reminders are not generated, because the scheduler maintenance pass does not run in the cloud.

### D6. Cloud destination policy

Package `internal/cloudtargetpolicy`, used only by the Lambda worker and the planner's monitor reconciliation.
Its behaviour mirrors `targetpolicy`'s structure:

- `STATUSFORGE_CLOUD_TARGETS` is an explicit allowlist of `https://host[:port]` origins; the default port is
  443. HTTP, userinfo, fragments, and hosts not on the list are refused.
- It is validated when the monitor is saved and again at dial time. The dialer resolves the host, refuses the
  connection if **any** resolved address is non-public, and dials the validated IP with TLS `ServerName` set to
  the host. It uses no proxy and follows no redirects, and keeps the existing header, body, and deadline bounds.
- Non-public ranges:
  - IPv4: loopback, RFC 1918, `100.64.0.0/10`, link-local `169.254.0.0/16` (which covers the instance metadata
    address `169.254.169.254` and Lambda's `169.254.100.1` metadata endpoint), `0.0.0.0/8`, `192.0.0.0/24`,
    documentation and benchmarking ranges, multicast, and `240.0.0.0/4`;
  - IPv6: loopback, unspecified, `fe80::/10`, `fc00::/7`, multicast, documentation ranges, and IPv4-mapped or
    NAT64 forms of any of the IPv4 ranges above.
- The host of `AWS_LAMBDA_RUNTIME_API` is always refused.
- An empty allowlist refuses every check (`checker_problem` / `refused_by_policy`). The committed default
  declares no targets.

### D7. Lambda binaries and isolation

- `cmd/lambda-planner` and `cmd/lambda-worker` are separate binaries, so each has its own IAM role.
- They are built with `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 -tags lambda.norpc` into a `bootstrap` executable
  for `provided.al2023`.
- Each `main` refuses to start unless `AWS_LAMBDA_RUNTIME_API` is set. Run on a workstation, it exits non-zero
  before reading configuration or credentials.
- `cmd/statusforge`'s dependency graph must not contain `internal/hosteddynamo`, `internal/cloudtargetpolicy`,
  `github.com/aws/aws-lambda-go`, `github.com/aws/aws-sdk-go-v2/config`, or `github.com/aws/aws-sdk-go-v2/service/sqs`.
  `make local-isolation-check` enforces this and runs inside `make backend-check`.

### D8. Declared monitors

The cloud has no management API. `STATUSFORGE_CLOUD_MONITORS` (JSON, validated, intervals ≥ 60 s, URLs checked
against D6) declares the monitors. Each planner pass reconciles them through the existing store save paths:

- monitors that are missing are created, which queues a `create` run;
- changed settings are updated, which bumps the version and queues a `config_change` run;
- monitors no longer declared are archived.

Reconciliation is idempotent, and an unchanged declaration writes nothing.

### D9. Local contract tests

- Go tests drive the handlers in process:
  - `aws-lambda-go` `events.SQSEvent` fixtures for the worker;
  - an injected clock and a fake SQS sender for the planner;
  - a DynamoDB Local scratch table (loopback).
- The tests simulate redelivery, concurrent duplicates, partial batches, send failures, expiry, and cold starts.
- No emulator container, account, or token is used.
- CDK template assertions cover the synthesized stack (D10).

### D10. Infrastructure and sandboxed synthesis

- **CDK project:** `infra/`, TypeScript, with its own lockfile. It uses an environment-agnostic stack, so it
  needs no `env` and does no lookups. Synthesis runs with `--lookups=false --no-notices --no-version-reporting`
  and `CDK_DISABLE_CLI_TELEMETRY=true`. CI synthesises with every `AWS_*` variable unset. No `bootstrap` or
  `deploy` target exists in the Makefile or CI.
- **Resources:**
  - one DynamoDB table: provisioned capacity, no autoscaling, TTL on `expiresAt`, point-in-time recovery off;
  - a standard SQS work queue with SSE-SQS and a dead-letter queue;
  - the planner and worker functions (arm64, 128 MB), each with an explicit log group, short retention, and a
    removal policy of destroy;
  - one schedule with an execution role;
  - least-privilege roles whose DynamoDB actions are derived from what the Go contract tests observe
    (`infra/iam/dynamodb-actions.json`).
- **Worker event source mapping:** `ReportBatchItemFailures`, a small batch size, maximum concurrency 2, and a
  visibility timeout of at least 6× the function timeout.
- **Sandbox:** unsetting `AWS_*` is not enough. The CDK CLI resolves a default account through the SDK
  credential chain, which also reads `~/.aws` files, SSO caches and instance metadata, and the CLI can fetch
  notices and version data. `make infra-synth` therefore runs the pinned CLI inside `unshare -rn` (loopback
  only) under `env -i` with a fresh temporary `HOME`. Without a working `unshare` the target fails; it never
  falls back. In-process synthesis without the CLI was considered and not chosen so that the committed template
  is what the CLI would actually produce.

The binding rules are in [cloud path](../architecture/cloud-path.md).

## Alternatives considered

- **One schedule per monitor:** the schedules must track monitor configuration, and gaps and expiry still need a
  reconciler. Rejected.
- **A single Lambda that plans and checks inline:** simplest, but it drops SQS and with it any retry, dead-letter
  queue, or duplicate-delivery evidence. Rejected.
- **Cloud-only orchestration of claim, run, and record:** lowest local risk, but two copies of the eligibility and
  deadline rules would drift. Rejected for D3.
- **Leaving notification intents pending:** misleading ("pending" implies a sender), and resolved incidents would
  never expire. Rejected for D5.
- **LocalStack or ElasticMQ for contract tests:** LocalStack now needs an account token; ElasticMQ adds a
  container and emulates only a subset of SQS. In-process fixtures test the adapters' own logic. Real SQS and
  Lambda semantics remain on the unverified list.
- **On-demand table billing:** simpler, but every request is billed and the stack must stay inside a known
  allowance.

## Consequences

- The local scheduler's worker is refactored onto `checkwork`; the scheduling and capacity tests were rerun
  after the refactor.
- A new direct dependency, `github.com/aws/aws-lambda-go` (Apache-2.0), plus the SDK `config` and `sqs` modules
  are used by the Lambda binaries only. Licence and vulnerability checks cover those binaries.
- Cloud coverage evidence consists of planner and worker log lines and the stored gaps; the local in-process
  coverage state has no cloud equivalent.
- Everything here is **prepared, not operated**: claims about SQS delivery, Lambda scaling, hosted DynamoDB
  throttling, TTL timing, IAM sufficiency, and cost remain unverified.
