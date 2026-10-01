# Testing and verification

How to run the checks, what each layer covers, and the principles the suites follow. Nothing here contacts
AWS or a live website; every test runs against loopback fixtures, `httptest` servers, or DynamoDB Local.

## Commands

| Command | What it runs |
| --- | --- |
| `make verify` | `backend-check`, `web-check`, `infra-check`: the authoritative local gate, the same one CI runs |
| `make backend-check` | `make local-isolation-check`; `gofumpt -l` and `golines -l -m 100` (both must be empty); `go vet ./...`; `go test -race -count=1 ./...`; `go build ./...` |
| `make web-check` | `eslint --max-warnings 0`, `tsc --noEmit` (app and node projects), `prettier --check`, `vitest run`, `vite build` |
| `make infra-check` | Prettier check, `tsc --noEmit`, Vitest template assertions, then `make infra-synth` (sandboxed CDK synthesis + template check) |
| `make security-check` | `govulncheck`, `npm audit --omit=dev` (web, infra), licence allowlist, `gitleaks` over tree and history; separate from `make verify` |
| `cd backend && go test -race -run 'TestName' ./internal/pkg` | One Go package or test |
| `npm --prefix web run test:watch` | Vitest watch mode |
| `make format` | `golines` + `gofumpt`, Prettier for web and infra |

The Go formatters are pinned tools in `backend/go.mod` (`go tool gofumpt`, `go tool golines`); no global
install is needed. `govulncheck` and `gitleaks` are pinned in `tools/go.mod`.

### Tests that need DynamoDB Local

`internal/store`, `internal/upgrade`, `internal/dataexport`, `internal/cloudwork`, and parts of
`internal/httpapi` run against the real DynamoDB Local container on `127.0.0.1:8000` (`make db-up`). Each
test creates its own scratch table and deletes it. When the container is unreachable these tests **skip with
an explicit message** rather than substituting an in-memory fake; `make verify` still passes, but the
integration layer has not run. CI always runs them (the `integration` job).

## What each layer covers

| Layer | Approach | Representative coverage |
| --- | --- | --- |
| Domain rules (`monitor`, `incident`, `heartbeat`, `summary`, `retention`, `targetpolicy`, `cloudtargetpolicy`) | Pure Go unit tests, fixed clocks, no I/O | Status evaluation order; incident thresholds and run clearing; deadline arithmetic and outage containment; slot attribution and gap spreading; percentile edge cases; URL validation order; public-address classification including NAT64 and metadata ranges |
| Store (`store`) | Integration against DynamoDB Local; concurrency tests with goroutines racing on one item | Conditional-write rejections; duplicate and stale results never overwrite status; lease takeover after expiry; gap/work/cursor atomicity; incident evaluation inside the result transaction; revision races; notification claim/complete; cursor paging stability under concurrent inserts; deletion without resurrection; legacy item shapes |
| Scheduler (`scheduler`) | Fake store and clock | Tick materialisation, catch-up as one gap, fairness under saturation, coverage state transitions, shutdown drain |
| Shared slot runner and cloud adapters (`checkwork`, `cloudwork`) | Scratch table, fake SQS sender, `aws-lambda-go` event fixtures, injected clock | Redelivery, concurrent duplicates, partial batches, send failure then re-dispatch, expiry, cold start; DynamoDB mid-batch unavailability; the IAM action list derived from observed calls (`iam_test.go`) |
| Outbound checks and delivery (`checker`, `notify`) | `httptest` servers and the fixtures | Timeout vs connection error vs unknown outcome classification; redirects not followed; body truncation; dial-time policy refusals; retry schedule and attempt accounting; the receiver fixture's `Idempotency-Key` handling (`notifyreceiver`) |
| HTTP API (`httpapi`) | `httptest` per route | Validation codes and field paths; 409 reasons; protection rules (Host, Origin, Content-Type, size); ingest ordering (size → auth → rate limit → lifecycle → validation); logs carry no token or URL |
| Upgrade (`upgrade`) | Import committed exports from earlier builds, run housekeeping to completion | Format marker and backfill; every fact in the manifest still returned by current readers; restart mid-backfill resumes; newer format refused |
| Web (`web/src`) | Vitest + Testing Library | Strict API payload validation; status headline wording per state; polling pauses when hidden; chart table equivalents; conflict handling (`409` reloads, never auto-retries); deletion confirmation; keyboard reachability of actions |
| Infrastructure (`infra/test`) | Vitest on the in-process template | Exact resource multiset, IAM rules, closed defaults, rejection of extra resources; the same rules run on the CLI output in `make infra-synth` |

Sizes at the time of writing: 72 Go test files (188 top-level test functions, most table-driven), 35 web test
files, one infra test file; `make verify` runs 27 Go packages with `-race`.

## CI

`.github/workflows/ci.yml` runs five jobs on every push and pull request:

- `backend`, `web`, `infra`: the three `make *-check` targets. The `infra` job has `permissions: contents:
  read`, no secrets, and no credentials action; synthesis runs inside `unshare -rn`.
- `security`: `make security-check` with full history checked out.
- `integration` (after backend and web): builds the API binary and drives it as a black box — starts it with
  DynamoDB Local **absent** and asserts live `200` / ready `503`; starts the container and asserts recovery,
  `dataFormat 6`, and TTL on `expiresAt`; runs the DynamoDB-backed Go packages; stops and restarts the
  container and asserts ready `200 → 503 → 200`; and asserts that a wildcard bind, a hosted endpoint, and a
  missing endpoint each exit `1` before listening.

## Principles

- **Behaviour, boundaries, and failure paths** — not wiring, wording, or incidental defaults. Log message
  text is not pinned.
- **Injected clocks and short intervals.** Scheduling and freshness tests never wait through production
  intervals; accelerated intervals must not change the semantics being tested.
- **No green by substitution.** A store test that cannot reach DynamoDB Local skips loudly; it never swaps in
  a fake to pass.
- **Real binaries for integration.** The CI integration job and the capacity driver exercise the built
  `statusforge` binary over HTTP, not handlers in process.
- **Evidence beyond tests.** The [capacity report](../operations/capacity-report.md) (restart and database
  outage under load, duplicate-slot audit on raw rows) and the [security review](../operations/security-review.md)
  (controls with their proving test or manual check) record what was actually exercised and where.

## Manual browser checks

Component tests are not an accessibility evaluation. User-visible changes are also driven in a real browser
at desktop width and at 320–390 px, with keyboard-only navigation and a `securitypolicyviolation` listener;
the [security review](../operations/security-review.md) records the last such pass.
