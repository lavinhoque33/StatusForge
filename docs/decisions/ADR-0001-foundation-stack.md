# ADR 0001: Foundation stack and local safety design

- Status: accepted (2026-09-27)

## Context

StatusForge is a local-first monitoring application with a fixed direction: Go with `net/http` and chi,
React/TypeScript, DynamoDB Local as the only database, a $0 budget, and no cloud access from local or CI
paths. This record fixes the exact toolchain, the repository layout, and the mechanism that makes a
misconfigured hosted endpoint or an unintended public binding impossible rather than merely discouraged.

## Decisions

### Versions (verified against official sources on 2026-09-27)

| Component | Choice | How verified |
| --- | --- | --- |
| Go | 1.27 | `https://go.dev/dl/?mode=json` listed 1.27.1 as stable; tarball sha256 matched |
| chi | v5.3.2 | `go get @latest` |
| AWS SDK for Go v2 | core v1.47.1, credentials v1.20.6, service/dynamodb v1.69.1 | `go get @latest` |
| Node / npm | 24.x / 11.x (`.nvmrc`, `engines`) | — |
| React | 19.3.0 | `npm view` latest |
| Vite / Vitest | 8.3.1 / 5.0.2 | `npm view` latest |
| TypeScript | 5.9.3 (held) | typescript-eslint 8.70.1 peer range `<6.1.0` excludes TypeScript 7 |
| ESLint / typescript-eslint / Prettier | 10.11.0 / 8.70.1 / 3.9.9 | `npm view` latest |
| DynamoDB Local | `amazon/dynamodb-local:3.3.1` | Docker Hub tag list; same digest as `latest` on 2026-09-27 |

Consequence: `go.mod`, `package.json`/`package-lock.json`, and `compose.yaml` are the version authority;
prose must not repeat pins beyond this record.

### Repository layout

Monorepo: `backend/` (one Go module with every binary, including fixtures), `web/`, `docs/`,
`infrastructure/scripts/`. No `fixtures/` directory: fixtures are Go binaries and live in the backend module
to avoid a second module. No application Dockerfiles: the local profile is native applications plus
containerized dependencies.

### Local safety design

1. **Loopback-only listeners, enforced in code.** `config.Load` rejects any `STATUSFORGE_HTTP_ADDR` or
   `STATUSFORGE_SAMPLE_TARGET_ADDR` whose host is not `127.0.0.0/8`, `::1`, or literal `localhost`; empty
   host (`:8080`) and wildcards are rejected before `net.Listen`. There is no override flag.
2. **Explicit loopback DynamoDB endpoint, no fallback.** `STATUSFORGE_DYNAMODB_ENDPOINT` is required and must
   be an `http(s)` URL with a loopback host and no userinfo/query/fragment. The client is built from a manual
   `aws.Config` with static placeholder credentials, `BaseEndpoint`, `RetryMaxAttempts: 1`, no HTTP proxy, and
   redirects disabled. `config.LoadDefaultConfig` is never called, so `AWS_*` variables, shared config files,
   and the instance metadata service are never consulted.
3. **Degraded, not dead.** The API starts even when DynamoDB is unavailable and reports it through
   `/api/health/ready` (503, coarse `reason`) and a warning log containing only the endpoint host.
4. **Same-origin only.** No CORS headers; the browser reaches the API only through the Vite `/api` proxy in
   development and through the embedded UI in the release build.
5. **Destructive reset is explicit.** `make db-reset CONFIRM=yes` is the only path that removes local data.

### DynamoDB Local persistence

`-sharedDb -dbPath -disableTelemetry` on a named volume. Because the image runs as uid 1000 and Docker creates
a new named volume root-owned, a one-shot `dynamodb-init` service (`chown` only) prepares the directory; the
database process itself never runs as root. Tables and items survive `restart` and `down`/`up`.

## Alternatives considered

- **`user: root` on the DynamoDB service** — simpler, rejected to keep the database process unprivileged.
- **Bind mount under the repo** — depends on host uid matching 1000; rejected as non-portable.
- **pnpm/bun** — available, but npm keeps the lockfile and CI story boring and universal.
- **golangci-lint** — deferred; `gofumpt` + `golines` + `go vet` + `-race` are enough until a real lint need
  appears.
- **Creating a table at startup** — deferred to the first persistence decision ([ADR 0002](ADR-0002-persistence.md))
  so the key design follows access patterns.

## Consequences

- Any future non-loopback profile (a hosted management surface) needs its own explicit configuration design
  and ADR; the guard is intentionally not overridable.
- Readiness probes are the only DynamoDB call in the foundation. Table and key design, conditional writes,
  and retention are decided in ADR 0002 and later records.
- CI stays within the free allowance and never touches AWS.
