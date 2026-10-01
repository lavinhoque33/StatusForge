# Contributing

StatusForge is a personal project; issues and pull requests are welcome, and the conventions below are what
the code already follows. Read [local setup](docs/development/local-setup.md) to get running and
[testing](docs/development/testing.md) for the checks.

## The gate

```sh
make verify          # backend, web and infra checks — the same gate CI runs
make security-check  # vulnerabilities, production licences, secrets (separate CI job)
make format          # golines + gofumpt, Prettier
```

`make verify` must pass before a pull request is opened. Store, upgrade, export, and cloud-path tests run
against DynamoDB Local (`make db-up`) and skip loudly without it; run them locally when touching those packages.

## Conventions

### Structure

- One Go application, one web client, one table. Backend code is packaged by product domain under
  `backend/internal/`; new packages only when a change needs them.
- Transport stays thin: handlers decode and validate the request shape, call the store or domain package,
  encode a response. Monitoring rules (eligibility, incident transitions, freshness, coverage) live in domain
  packages that import neither `net/http`, chi, nor the AWS SDK.
- The web client presents backend state. It never maintains a second copy of incident or freshness rules and
  never reinterprets an unknown state as healthy.
- Interfaces exist at genuine test or change boundaries only. No ORM, cache, broker, or job framework.

### Monitoring truth

- Keep the state dimensions separate: observed healthy, observed failing, stale/unknown, paused, checker
  problem, delivery problem. Unknown is never presented as healthy.
- State changes need eligible evidence: an older observation never overwrites a newer state; results from a
  superseded configuration, duplicate work, a paused period, or a manual check are recorded but not counted.
- Reports state their window, denominator, and treatment of pauses and gaps. Missing intervals are neither
  success nor failure.

### Durability and concurrency

- Pending work, observations, incident transitions, and notification intents are durable rows; process memory
  is never the only record of an obligation.
- State transitions and work acquisition use conditional writes; never read-modify-write without a condition.
  Define the idempotency key and the duplicate-safe effect before adding a retry.
- A completed check that observed a failure is evidence; a processing failure before the result was recorded
  is infrastructure. Infrastructure retries never rewrite an unfavourable observation.
- Every goroutine has an owner, a cancellation path, and a bounded lifetime. Pass `context.Context` to all
  blocking work. Pools are fixed-size and configured. Tests run with `-race`.

### Outbound safety and the local boundary

- Every outbound request has a deadline, a bounded body read, no redirects, and a scheme/host/port allowlist
  checked at save time and again at dial time.
- Listeners bind to loopback; configuration rejects non-loopback addresses and non-loopback database endpoints
  with no fallback. The backend reads only `STATUSFORGE_*` variables, each documented in `.env.example`.
- Never log credentials, authorization headers, secret URLs, target response bodies, or endpoint URLs.
- The local binary must not link cloud-only packages (`make local-isolation-check` enforces it).

### Time

- UTC instants for events, monotonic time for durations. Inject a clock into anything that schedules,
  expires, or judges freshness. Distinguish due, started, completed, and received times.

### Web

- Responsive from 320 px; semantic landmarks, native controls, visible focus, 44 px targets, reduced motion
  respected. Status is never colour alone. Charts have table equivalents. Times show zone and age.

### Tests

- Test behaviour and invariants, not wiring or wording; do not pin log text.
- Priorities: eligibility rules, incident transitions, duplicate/stale/out-of-order work, cancellation and
  deadlines, restart recovery, retained delivery intent, destination-policy rejections.
- Domain rules: unit tests with fakes and fixed clocks. Store: DynamoDB Local. Handlers and targets: `httptest`.
  Web: Vitest + Testing Library for meaningful behaviour. Never contact live sites or AWS.
- Never swap in a fake to make a missing DynamoDB Local look green; skip with a message.

### Formatting and dependencies

- Go: `gofumpt` and `golines` at 100 columns (pinned `tool` entries in `backend/go.mod`); `go vet` clean.
  Web and infra: Prettier, ESLint with zero warnings, strict TypeScript.
- Pin dependencies exactly; review lockfile diffs; `make security-check` must stay green (licence allowlist,
  production audit).

### Documentation

- A change to a command, port, variable, route, or error code updates the matching document in `docs/`.
- Consequential trade-offs get an ADR under `docs/decisions/` with context, decision, rejected alternatives,
  and consequences. Routine choices do not.
- Capabilities not yet verified in an environment are labelled as such; never state hosted-AWS behaviour as
  fact from local evidence.

## Commit messages

Conventional prefixes (`feat:`, `fix:`, `ci:`, `docs:`, `chore:`), imperative mood, a body that says why
when the diff does not.
