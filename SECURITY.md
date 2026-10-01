# Security

StatusForge is a single-user, local-only tool. Its threat model, the controls in force, the tests that prove
each, and the known gaps are documented in [docs/operations/security-review.md](docs/operations/security-review.md).

## Scope

In scope: anything that lets a non-loopback client reach the API, a request escape the destination policy
(non-allow-listed host, redirect, private or metadata address on the cloud path), a token or body appear in
logs, an unauthenticated ingest write, a stale or duplicate result overwrite newer state, or the local binary
contact AWS.

Out of scope: the absence of authentication on the loopback management API (documented and intentional),
and anything requiring the API to be exposed beyond loopback.

## Reporting

Open a private security advisory on this repository (GitHub → Security → Report a vulnerability) rather than
a public issue. Include the route or command, the observed behaviour, and the expected behaviour. Reports are
answered on a best-effort basis; this is a personal project with no release cadence or SLA.

## Dependency and secret scanning

`make security-check` runs `govulncheck`, production `npm audit` (web and infra), a licence allowlist, and
`gitleaks` over the working tree and full history. It runs as its own CI job. Accepted, version-pinned risks
are listed with their reasoning in the security review; the check fails if the pinned version changes.
