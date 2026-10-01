# StatusForge documentation

Start with the [README](../README.md) for what the product does and how to run it, then
[engineering notes](engineering.md) for the five design problems worth reading the code for.

## Architecture

| Document | Covers |
| --- | --- |
| [System overview](architecture/system-overview.md) | Context diagram, the claim → check → record flow, package map, boundaries |
| [Data model](architecture/data-model.md) | Single-table DynamoDB layout, item catalogue with lifetimes, conditional-write contracts, format marker and upgrades |
| [Scheduling](architecture/scheduling.md) | Durable work items, leases, gaps, the tick cycle, fairness, coverage state, measured limits |
| [Incidents and notifications](architecture/incidents-and-notifications.md) | Incident state machine inside the result transaction, outbox delivery, retries, reminders, maintenance |
| [Heartbeats and deployments](architecture/heartbeats-and-deployments.md) | Token-authenticated ingest, deadline slots, receive-outage vs missed heartbeat, deployment markers |
| [Summaries and overview](architecture/summaries-and-overview.md) | Slot-based coverage arithmetic, latency percentiles, bucket charts, the composed Overview |
| [HTTP API](architecture/http-api.md) | Route catalogue, error shapes, protection rules, target policy |
| [Cloud path](architecture/cloud-path.md) | Planner/worker Lambdas, SQS, hosted-store seam, destination policy, synth-only CDK stack, IAM derived from code |

## Decisions

Architecture decision records, in order. Each states the context, the decision, what was rejected, and
consequences; superseded points are marked in place.

1. [Foundation stack](decisions/ADR-0001-foundation-stack.md) — Go, React, DynamoDB Local, loopback-only
2. [Persistence](decisions/ADR-0002-persistence.md) — single table, versioned monitors, status on read
3. [Scheduling persistence](decisions/ADR-0003-scheduling-persistence.md) — work items, leases, gaps, freshness
4. [Incidents and notifications](decisions/ADR-0004-incidents-notifications.md) — transactional evaluation, outbox, retries
5. [Heartbeats and deployments](decisions/ADR-0005-heartbeats-deployments.md) — tokens, liveness, markers
6. [Summaries and overview](decisions/ADR-0006-summaries-overview.md) — coverage not uptime, bounded reads
7. [Retention and release](decisions/ADR-0007-retention-and-release.md) — TTL, housekeeping, deletion, single binary
8. [Cloud path](decisions/ADR-0008-cloud-path.md) — prepared not operated, isolation, closed defaults

## Operations

| Document | Covers |
| --- | --- |
| [Local runbook](operations/local-runbook.md) | Start, inspect, stop, retention, export/import, deletion, upgrade, demo, capacity, security check, recovery scenarios |
| [Capacity report](operations/capacity-report.md) | Measured throughput, gaps, latency, restart and outage behaviour on one workstation; the dispatch-collapse diagnosis |
| [Security review](operations/security-review.md) | Controls with the test or manual check proving each, dependency gates, accepted risks, known gaps |

## Development

| Document | Covers |
| --- | --- |
| [Local setup](development/local-setup.md) | Prerequisites, first run, every configuration variable, ports, fixtures |
| [Testing](development/testing.md) | Commands, what each layer covers, CI jobs, principles |
| [Contributing](../CONTRIBUTING.md) | Conventions and the pre-submit gate |

## Product

| Document | Covers |
| --- | --- |
| [Terminology](product/terminology.md) | Monitor, check, observation, work item, gap, incident, counted, freshness, coverage |
| [Changelog](../CHANGELOG.md) | Releases |
