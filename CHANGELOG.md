# Changelog

## Unreleased

### Web client

- The web client is restyled in light and dark schemes with a layered design system: design tokens and cascade
  layers, a sidebar shell (a top header below 1024 px, a navigation grid below 520 px), card headers with icons,
  tone rails, shape-coded state pills, and tables that stack on small screens. A "Skip to content" link moves focus
  to the page heading. Wording, roles and behaviour are unchanged. Icons are self-authored inline SVG.
- The monitor summary gets a segmented window control and a stat strip above the unchanged sentences. Its charts
  gain patterned stacked status bars, labelled outage and paused bands, a latency chart with a gradient median
  area, an isolated-point marker and a labelled check deadline, and token-coloured tooltips. The chart code is
  still a separate lazy chunk.
- Summary charts shade a paused span and a touching receive outage separately, each in its own tone with its own
  "Paused" or "Outage" label. They were merged into one outage-coloured "Outage + pause" band, which painted paused
  time as an outage. Band labels now stack in rows, so nearby labels never cover each other or the check-deadline
  label.
- The UI bundles the Inter variable font (`@fontsource-variable/inter`, SIL Open Font License 1.1) as same-origin
  woff2 files, so it renders the same on every OS. Vite never inlines font files, so the CSP's `font-src 'self'`
  holds. The licence text ships at `/fonts/Inter-LICENSE.txt`, and `licence-exceptions.json` records the reasoned
  `OFL-1.1` exception.

### Demo

- `make demo` seeds a varied week: five monitors in one application, per-service response-time profiles with daily
  cycles and spikes, isolated failures and timeouts below the incident threshold, a checker-problem burst, a past
  pause, a worker that is paused now, and irregular late and missed heartbeats. A fixed seed keeps the scene the
  same on every run, and the seed fails unless its self-check finds every feature. Seeded error responses carry the
  checker's own `wrong_status` reason, so they count as latency samples, as live ones do. The open incident gets
  its reminders as the live scheduler would send them (every 6 h by default). Seeding takes about four and a half
  minutes.
- The README shows twenty new captures of that scene, including dark-scheme and 390 px views.

### Toolchain

- Local setup asks for the newest Go 1.27 patch, the one CI installs. On go1.27.1, `govulncheck` in
  `make security-check` reports four standard-library `net/http` advisories fixed in 1.27.2.

### Cloud path (code and synthesis only; nothing deployed)

- `lambda-planner` and `lambda-worker` binaries (`make lambda-build`, linux/arm64) reuse the store and domain
  packages: the planner reconciles declared monitors, runs the tick cycle and a retention step, and sends SQS
  work messages; the worker runs each message through the shared `checkwork.RunSlot`. Redelivery and
  duplicates are safe because the durable work row decides eligibility.
- Hosted-table validation (`hosteddynamo`) that never creates or alters tables; a cloud destination policy with
  public-address checks and pinned TLS dialing; "no channel" notification intents.
- The local scheduler now runs each slot through the same `checkwork` runner (no behaviour change).
  `make backend-check` includes `local-isolation-check`, which fails if the local binary links a cloud-only
  package.
- `infra/`: a synth-only TypeScript CDK app defining one environment-agnostic `StatusForge` stack — a
  provisioned table, three queues, planner and worker Lambdas, explicit log groups, a schedule that is
  `DISABLED` by default, and three inline-policy roles. `make infra-synth` runs `cdk synth` inside a
  no-network, no-credential sandbox (`unshare -rn`, `env -i`, throwaway `HOME`) and checks the template
  against the resource and IAM rules in `docs/architecture/cloud-path.md`; `make infra-check` is part of
  `make verify`, and CI gains an `infra` job with read-only permissions.
- The DynamoDB IAM action list is recorded from the cloud-path contract tests
  (`infra/iam/dynamodb-actions.json`); a Go test fails when code calls an action the roles do not grant.
- `make security-check` audits and licence-checks `infra/` and the Lambda binaries. One version-pinned risk
  is accepted and documented: `brace-expansion` 5.0.9 bundled in `aws-cdk-lib`, used only at synthesis.
- DynamoDB Local starts with `-disableTelemetry` (`compose.yaml`); earlier releases let it send usage
  telemetry in the background. The next `make db-up` recreates the container; data stays on the volume.

## 0.6.0 — local release (2026-09-29)

- Retention periods with DynamoDB TTL, resumable incident retention and format-6 backfill, permanent deletion
  of archived monitors and applications, private export/import, and a Settings page.
- Single Go binary with the embedded web UI, history fallback, build version in CLI/API/UI, and a demo-data
  banner.
- Loopback `Host` and mutation `Origin`/body protections, Content Security Policy, `make security-check`, a
  reproducible demo, and a bounded capacity report.
- Response-time axis labels switch to seconds at 1 s or more, matching the summary sentence.
- Demo history ends when seeding starts; demo notifications are delivered at their transitions with real HTTP
  statuses.
- A request the browser aborts is logged at INFO with `client_canceled=true`, not as an ERROR.
- Wide tables scroll from the keyboard as named focusable regions; client-side navigation moves focus to the
  new heading and sets the page title; the date-time picker shows its focus ring.
- `make doctor` reports Python 3 as optional; local setup gains a first-monitor walkthrough and a note that
  checkouts share local data.
- **The scheduler no longer loses most scheduled checks under load.** Workers take work as soon as they are
  free and never receive work that has already expired; each scheduling pass lists monitors once and works on
  them with bounded concurrency. On the measured workstation, 100 monitors at 10 s went from 12 completed
  checks in 10 minutes to every slot covered; 100 monitors at 60 s went from 63 gap slots in 5 minutes to 0.
- **Scheduler coverage is visible.** `GET /api/system/scheduler` and the Overview report `unknown`,
  `disabled`, `ok`, or `behind`; the Overview shows **Scheduler is behind** with guidance; the log adds
  counts to `scheduler tick` plus `scheduler coverage degraded` / `recovered`.
- Known limit: about 10 scheduled checks per second on DynamoDB Local on the measured workstation. At 250
  monitors at 10 s about 39 % of checks ran; the rest were recorded as gaps. See
  `docs/operations/capacity-report.md`.

## Earlier

Heartbeats and applications with deployment markers; incidents, notifications with retries and reminders,
maintenance windows; durable scheduling with leases and gaps; 24 h / 7 d summaries, history paging and
filters; the single-table DynamoDB persistence model; and the loopback-only foundation. The history is in
`git log` and the decisions in `docs/decisions/`.
