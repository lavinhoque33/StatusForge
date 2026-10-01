# Capacity report (local workstation)

All figures come from one local workstation running DynamoDB Local. They show what this machine did; they are
not a service-level objective and not an estimate for hosted DynamoDB. No AWS credentials or hosted endpoints were
used. The only monitored target was the loopback sample fixture.

## Current results (after the dispatch fix, 2026-09-29)

### What the fix changed

- **Continuous dispatch.** Workers take the next eligible check as soon as they are free. There is no longer a
  cap of `STATUSFORGE_WORKERS` checks per scheduler cycle.
- **No expired hand-offs.** A check that has passed `dueAt + interval` is never handed to a worker; the store
  records it as one `overdue` gap. For each monitor the scheduler offers the freshest eligible slot, and the
  least-recently-claimed monitor goes first.
- **Cheaper cycle.** One monitor list per cycle. Per-monitor store work runs with at most 8 monitors in flight
  and a 5 s timeout each, inside a 15 s cycle.
- **Visible shortfall.** A coverage state (`GET /api/system/scheduler`, and `scheduler` on the Overview API)
  reads `behind` when more than 5% of the due checks in the last 5 minutes were missed. The Overview then shows
  **Scheduler is behind**, and the log has WARN `scheduler coverage degraded` and INFO
  `scheduler coverage recovered`. Readiness still means "dependencies answer", nothing more. See the
  [runbook](local-runbook.md#scheduler-is-behind).

### Reproduce

From the repository root, with the existing DynamoDB Local container already running on `127.0.0.1:8000`:

```sh
cd backend
go run ./cmd/capacity -duration 10m -stages 25,100,250,restart,outage -out /tmp/statusforge-capacity.json
```

The run took place on 2026-09-29, 04:44:49–05:35:18 UTC and exited **0** after 50 min 34 s.
- Built from the working tree containing the fix, before it was committed.
- Setup: the same driver, ports, proxy, machine, Go and DynamoDB Local versions as the earlier run below;
  `STATUSFORGE_WORKERS=4`; 10-second intervals; 600 seconds per stage.
- The driver now also samples `GET /api/system/scheduler` every 10 seconds (`samples[].scheduler`,
  `stages[].schedulerStates`). Optional `-workers` and `-api-log-level` flags exist for diagnostics; release
  evidence uses their defaults.
- Each scratch table was deleted after its stage. The database container was not stopped, restarted or reset.

### Results at 10-second intervals

`Recorded checks` includes one create run per monitor, so it can exceed the expected count. Delays are
`startedAt − dueAt` in milliseconds; GET latencies are p50 / p95 in milliseconds.

| Stage (10 min) | Expected checks/min | Recorded checks | Checks/min | Gap rows | Missing slots | Duplicate slots | Delay p50 / p95 / max | Overview GET (samples) | 24 h summary GET | Coverage state samples |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 25 | 150 | 1,525 | 152.5 | 0 | 0 | 0 | 1,238 / 2,143 / 4,108 | 94.1 / 115.1 (60) | 22.9 / 29.5 | `ok` 60 |
| 100 | 600 | 6,107 | 610.7 | 0 | 0 | 0 | 1,975 / 2,794 / 7,900 | 349.4 / 1,405.4 (60) | 21.5 / 29.0 | `ok` 60 |
| 250 | 1,500 | 6,072 | 607.2 | 8,817 | 8,817 | 0 | 8,328 / 9,895 / 9,999 | 747.6 / 2,720.8 (5 of 60) | 258.1 / 417.9 | `behind` 60 |
| 100 + API restart | 600 | 6,100 | 610.0 | 0 | 0 | 0 | 1,955 / 2,782 / 8,143 | 302.9 / 919.3 (59) | 19.2 / 146.8 | `ok` 59 |
| 100 + DB proxy outage | 600 | 5,859 | 585.9 | 135 | 248 | 0 | 1,986 / 2,854 / 9,989 | 297.8 / 1,032.4 (57) | 19.2 / 179.6 | `ok` 60 |

Before the fix (below), the 100-monitor stage recorded 12 checks and the 25-monitor stage 1,034.

Sampled resources, as **sample p50 / p95 / max** (CPU is percent of one logical CPU; RSS is MiB):

| Stage | API CPU % | API RSS MiB | DynamoDB Local CPU % | DynamoDB Local RSS MiB |
| --- | ---: | ---: | ---: | ---: |
| 25 | 8.0 / 9.2 / 9.8 | 22.8 / 23.2 / 23.5 | 6.3 / 16.1 / 18.0 | 340.9 / 341.4 / 341.5 |
| 100 | 23.4 / 28.6 / 33.2 | 24.0 / 24.9 / 25.1 | 6.4 / 72.0 / 74.3 | 349.5 / 350.6 / 350.8 |
| 250 | 29.1 / 33.3 / 69.0 | 27.2 / 28.5 / 30.4 | 65.7 / 76.7 / 96.5 | 444.4 / 446.5 / 447.1 |
| 100 + restart | 22.8 / 26.9 / 30.7 | 23.9 / 24.5 / 26.4 | 3.8 / 66.4 / 84.1 | 428.6 / 428.8 / 442.3 |
| 100 + outage | 22.6 / 26.9 / 31.4 | 24.5 / 25.4 / 26.8 | 3.8 / 61.4 / 74.3 | 423.2 / 423.8 / 440.8 |

What the stages show:

- **25 and 100 monitors keep up.** Every scheduled slot has an observation, with no gaps, no duplicates and
  coverage `ok` in every sample.
  - The delay p50 of about 2 s at 100 monitors is mostly the 2 s scheduling pause: work for a slot is created on
    the first cycle after its `dueAt`.
- **250 monitors at 10 s do not keep up**, and the application says so.
  - Throughput stayed at about 607 checks/min, the same as at 100 monitors: about 39% of the ~15,000 scheduled
    slots. The other 8,817 slots are `overdue` gaps.
  - Throughput per minute swung between 145 and 1,034.
  - Coverage read `behind` in all 60 samples.
  - The Overview answered within the driver's 3 s client timeout in only 5 of 60 samples.
  - DynamoDB Local CPU reached 96.5%.
  - The limit is the store work per check: the claim (a read plus a transaction) and the result transaction on
    single-node DynamoDB Local. Worker count is not the limit. A 3-minute diagnostic with 12 workers reached 46.3% of slots, confirming
    that worker count is not the limit.
- **API restart:** the API was stopped and a fresh process started at 05:20:12 UTC. Readiness was `200` again on
  the next 10-second probe (9.90 s, bounded by probe cadence). There were no gaps and no duplicates; pending work
  was picked up by the new process.
- **Database proxy outage** (05:30:16–05:30:46 UTC, container untouched):
  - Readiness went `200 → 503 → 200`, with the first ready probe in the same polling iteration as the
    restoration.
  - All 135 gap rows (248 slots) were recorded in the disruption window, and the minute containing the outage
    recorded 361 checks instead of about 600.
  - Coverage stayed `ok`, peaking at 35 missed of about 2,780 due. It counts dispatch misses (`overdue`); slots
    that could not even be scheduled while the database was unreachable become `not_scheduled` gaps. The outage
    itself shows as readiness `503`.

### Default configuration (60 s intervals, 4 workers)

A separate measurement ran HTTP monitors at 60 s against the loopback `/healthy` fixture for 5 minutes per
level. It used scratch tables, since deleted. Counts come from the API's `check` and `scheduler tick` log lines,
and the coverage state was read at the end of each level.

| Monitors at 60 s | Scheduled slots in 5 min | Checks completed (incl. one create run each) | Slots closed as gaps | Skipped expired hand-offs | Longest cycle | Coverage at end |
| ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 100 | 500 | 599 | 0 | 0 | 491 ms | `ok` (582 due, 0 missed) |
| 250 | 1,250 | 1,513 | 0 | 0 | 1,307 ms | `ok` (1,364 due, 0 missed) |

Before the fix, 100 monitors at 60 s recorded 63 gap slots in 5 minutes.

### Tested envelope

On this workstation, one StatusForge process on DynamoDB Local sustained about **10 scheduled checks per
second** (610 per minute). Tested with no gaps:
- 100 monitors at 10 s;
- 250 monitors at 60 s (the default configuration).

Loads above about 10 checks per second are not covered: they show **Scheduler is behind** and record the missed
slots as gaps. These are single runs per level, not measured maxima. Larger monitor counts at 60 s were not
tested and are not claimed.

### Limits and interpretation

- The coverage state is **this process's own evidence**, over the last 5 minutes.
  - It reads `unknown` after start until a due check resolves, and `disabled` when the scheduler is off.
  - Slots due before the process started, and retried attempts, are not counted.
  - So `ok` shortly after a restart says nothing about the downtime; the downtime is shown by its
    `not_scheduled` gaps.
- The state counts dispatch misses (`overdue`), not slots that could not be scheduled during a database outage.
  Those are `not_scheduled` gaps, and readiness is `503` while the outage lasts.
- One run per stage on one workstation, with the fixture on the same host and a populated persistent DynamoDB
  Local volume. Percentiles depend on the 10-second sampling cadence.
- **Nothing here claims hosted DynamoDB capacity, throttling, recovery time, or pricing.**

## Before the fix: dispatch collapse (2026-09-28)

The rest of this report is the evidence that led to the fix. It describes a scheduler that no longer exists, and is kept because the diagnosis explains the current design.

**Measured 2026-09-28, on one local workstation.** This is a bound on this machine and DynamoDB Local, not a service-level objective or an estimate for hosted DynamoDB. No AWS credentials or hosted endpoints were used. The only monitored target was the loopback `/healthy` sample fixture.

### Reproduce

From the repository root, with the existing DynamoDB Local container already running on `127.0.0.1:8000`:

```sh
cd backend
go run ./cmd/capacity -duration 10m -stages 25,100,250,restart,outage -out /tmp/statusforge-capacity-before.json
```

The command exited **0** after 50 min 20 s (21:11:19–22:01:39 UTC). It built the API and sample fixture, started both on loopback, and used a loopback TCP proxy on `127.0.0.1:8202` between the API and DynamoDB Local. The API was bound to `127.0.0.1:8200`, the fixture to `127.0.0.1:8201`. It created 25, 100, and 250 HTTP monitors at 10-second intervals in separate scratch tables; `restart` and `outage` each used another separate 100-monitor table. Each stage was held for **600 seconds** with `STATUSFORGE_WORKERS=4` (the default). Each table was deleted after its stage; the driver awaited `TableNotExists` before continuing. The database container was **not** stopped, restarted, or reset. The outage cut the proxy for 30 seconds and closed its established connections, then restored forwarding to `127.0.0.1:8000`.

Machine: AMD Ryzen 5 PRO 4650U with Radeon Graphics, 12 logical cores, 15,677,044 kB RAM (~14.95 GiB), Linux 7.0.0-31-generic x86_64. Go `go1.27.1`; DynamoDB Local image `amazon/dynamodb-local:3.3.1` (`compose.yaml`); The build was from the working tree, not a published tag.

### Results

Expected checks/min = monitor count × 6. Throughput and gaps are measured from raw table rows after the API was stopped for each stage. Scheduling delay is `startedAt − dueAt` on recorded scheduled observations (not on missing slots). p50/p95 use nearest-rank empirical percentiles; all times below are milliseconds. `Checks/min` is the observed count divided by the nominal 10-minute window. Gap rows can cover more than one missed slot. The raw `(monitor PK, dueAt)` observation pairs were checked for duplicates.

| Stage (10 min) | Expected checks/min | Recorded checks | Checks/min | Gap rows | Missing slots in gaps | Duplicate slots | Delay p50 / p95 / max (ms) | Overview GET p50 / p95 (ms) | 24 h summary GET p50 / p95 (ms) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 25 | 150 | 1,034 | 103.4 | 480 | 480 | 0 | 5,196 / 9,465 / 9,989 | 82.9 / 181.4 | 20.7 / 29.3 |
| 100 | 600 | 12 | 1.2 | 5,984 | 5,984 | 0 | 5,038 / 7,886 / 7,886 | 269.9 / 776.4 | 19.5 / 62.4 |
| 250 | 1,500 | 8 | 0.8 | 12,049 | 12,353 | 0 | 4,288 / 9,079 / 9,079 | 1,887.8 / 2,106.5 | 23.6 / 85.4 |
| 100 + API restart | 600 | 12 | 1.2 | 5,983 | 5,983 | 0 | 5,204 / 8,135 / 8,135 | 266.6 / 741.7 | 19.1 / 76.0 |
| 100 + DB proxy outage | 600 | 12 | 1.2 | 5,880 | 5,992 | 0 | 5,289 / 8,173 / 8,173 | 245.8 / 729.2 | 16.9 / 72.0 |

The driver sampled every 10 seconds. CPU is percent of one logical CPU (`docker stats` may show >100% for multiple cores); RSS is MiB. The following values are **sample p50 / p95 / max**, not process lifetime peaks:

| Stage | API CPU % | API RSS MiB | DynamoDB Local CPU % | DynamoDB Local RSS MiB |
| --- | ---: | ---: | ---: | ---: |
| 25 | 7.3 / 8.8 / 9.2 | 21.0 / 21.7 / 21.9 | 4.9 / 24.9 / 28.0 | 444.6 / 477.5 / 477.6 |
| 100 | 16.2 / 17.6 / 17.8 | 22.4 / 23.1 / 23.3 | 12.9 / 51.6 / 53.3 | 447.7 / 448.0 / 448.0 |
| 250 | 19.1 / 23.3 / 33.1 | 22.6 / 23.2 / 23.6 | 30.9 / 50.7 / 53.6 | 573.9 / 574.3 / 574.3 |
| 100 + restart | 16.0 / 17.8 / 18.7 | 21.9 / 22.4 / 22.6 | 11.3 / 48.5 / 56.6 | 456.8 / 464.8 / 631.0 |
| 100 + outage | 15.8 / 17.6 / 18.7 | 22.4 / 23.0 / 23.6 | 12.8 / 53.0 / 54.9 | 458.6 / 458.6 / 465.4 |

API CPU was calculated from `/proc/<API PID>/stat` CPU-tick deltas (100 Hz on this machine) divided by wall time; API RSS and DynamoDB Local RSS were read from `/proc/<PID>/status` (`VmRSS`), with the container process PID from read-only `docker inspect`. DynamoDB Local CPU came from read-only `docker stats --no-stream`. The GETs were issued every 10 seconds while ready; at the 25/100/250 loads, Overview had 59/60/60 successful samples and the summary had 60/60/60. During restart those counts were 59/59; during proxy outage, 57/57. A failed GET is not silently converted into a latency sample.

### Restart and outage observations

| Scenario | Interruption | Readiness sequence (10 s probes) | First observed ready after restoration | Gaps recorded (entire stage) | Raw duplicate observations per slot |
| --- | --- | --- | ---: | ---: | ---: |
| API restart at 100 monitors | API process stopped and a fresh process started at 21:46:34 UTC | 200 → no HTTP response (`0`) at 21:46:34 → 200 at 21:46:44 | 9.99 s after process start | 5,983 | 0 |
| Database proxy outage at 100 monitors | Proxy closed established connections at 21:56:37, restored at 21:57:07 UTC; container never stopped | 200 → 503 at 21:56:37 → 200 at 21:57:07 | Same probe as restoration (bounded by 10 s cadence) | 5,880 | 0 |

The outage's `restoredAt` and first sampled ready-at are both 21:57:07 (the same 10-second polling iteration). The original driver's `10.00 s` recovery field is incorrect: it used zero as an \"unmeasured\" sentinel, so a first measurement of zero was overwritten by the next 10-second probe. Readiness was already `200` at the first probe after restoration; the probe cadence cannot resolve its exact recovery time. The restart's ~9.99 s is also bounded by probe cadence rather than a precise startup latency. Both scenarios recorded many coverage gaps and no duplicate observation per `(monitor, dueAt)`; the initial driver does not attribute individual gaps to the 30-second disruption versus the already severe baseline under 100 monitors.

#### Supplemental outage window measurement

After observing that the original run counted gaps only over each entire stage, the driver was extended to emit per-minute completed checks and to count raw `GAP#` rows whose **recordedAt** falls from proxy cut through two minutes after restoration. This is a recording-time window, **not** proof that each gap's due slot was during the outage. The following exact command ran on the same workstation, with another dedicated scratch table, and exited 0 after 10 min 4 s (22:03:05–22:13:08 UTC):

```sh
cd backend
go run ./cmd/capacity -duration 10m -stages outage -out /tmp/statusforge-capacity-before-outage.json
```

Measured: 100 monitors, 12 observations (all in minute 1; following nine minutes each recorded **zero**), 5,849 gap rows covering 5,984 missing slots, and 0 duplicate `(monitor, dueAt)` pairs. Of these, **1,367 gap rows were recorded** from 22:08:06 UTC (proxy cut) through 22:10:36 UTC (two minutes after proxy restore at 22:08:36); this includes baseline gaps around the disruption. Readiness transitioned `200 → 503 → 200`, with the first ready probe in the same polling iteration as restoration. The supplemental binary still had the zero-sentinel recovery-time defect described above; its output's `10.00 s` must not be used as a recovery-time estimate. The driver source now records `recoveredAt` exactly once, including a same-iteration recovery.

In a **90-second recovery-timestamp smoke** (`cd backend && go run ./cmd/capacity -duration 90s -stages outage -out /tmp/statusforge-capacity-before-recovery-smoke.json`, exit 0), the corrected driver observed `503` at 22:16:18 UTC and `200` on the restoration probe at 22:16:48 UTC. It recorded `recoveredAt=22:16:48.964333887Z`, **3.14 ms** after `restoredAt=22:16:48.961196921Z`. This is the elapsed time to the *observed readiness probe*, not a bound on when the database connection internally became usable. Its scratch table was deleted.

### Diagnosis: scheduler dispatch collapse

A follow-up diagnostic shows why throughput collapsed: the limit is the scheduler's
dispatch design, not DynamoDB Local write capacity.

The diagnostic setup:
- 100 monitors at 10 s for 90 s on the same workstation;
- the API at log level `info`, since the driver pins `error`;
- a scratch table, deleted afterwards.

What it measured:
- **Timeline.** The first three scheduler cycles each dispatched exactly 4 checks. Every later cycle dispatched
  none, while each tick still created about 35 work items and closed about 30 as gaps. One tick in 90 s
  failed; the rest succeeded.
- **Work item outcomes.** Of the stage's WORK# items:
  - 862 are `missed`, all with `attempts = 0` and `closedReason = overdue`;
  - 96 are still pending;
  - 12 are `done`.

  No missed item was ever claimed.

The mechanism, from `backend/internal/scheduler/scheduler.go` and `backend/internal/store/scheduling.go`:
1. Each cycle is serial. It runs `Tick` (a scan, then per monitor a transaction, a `WORK#` query and closes),
   then `List` again, then per-monitor `Boundary` and `Works` queries. At 100 monitors this took about 3.5 s,
   followed by a 2 s pause.
2. Only then does the scheduler dispatch, and it sends at most `STATUSFORGE_WORKERS` (4) items per cycle. The
   ceiling is therefore about 4 / (2 s + cycle time), roughly 120 checks/min even at 25 monitors (103.4
   measured).
3. Candidates are ordered oldest `dueAt` first. By the time a worker claims them, the oldest have passed
   `dueAt + interval`, so `Claim` closes each one as `overdue`. It returns `ErrNotEligible` without a log line.
   The 4 dispatch slots per cycle are spent on expired work, and fresh slots never reach a worker.

Monitoring truth holds: every uncovered slot becomes a gap, there are no duplicate observations, and nothing is
presented as a completed check. But readiness stays `200` while coverage is near zero, and the logs say nothing
about the skipped claims.

The defaults are a 60 s minimum interval and 4 workers, and the same ceiling of about 4 dispatches per cycle
applies. A supplementary measurement ran the default configuration for 5 minutes per level. It used
HTTP monitors at 60 s, `STATUSFORGE_WORKERS=4`, the same loopback fixture, and scratch tables since deleted.
Counts come from the API's `check` and tick `gaps_created` log lines:

| Monitors at 60 s | Scheduled slots in 5 min | Checks completed | Slots closed as gaps |
| --- | --- | --- | --- |
| 50 | 250 | 299 (includes each monitor's first check on creation) | 0 |
| 100 | 500 | 471 | 63 |

So on this workstation the default configuration keeps up at 50 monitors and already drops slots at 100. Treat
about 50 monitors at 60 s as the tested envelope. This is one short run per level, not a measured maximum.

### Limits and interpretation (before the fix)

- **These load levels did not keep up.** Even 25 monitors recorded only 103.4 checks/min versus 150 expected and 480 explicit missing slots in 10 minutes. At 100 and 250, almost no checks completed despite readiness remaining `200` outside the planned outage. A healthy readiness response means the dependency answers, **not** that scheduled coverage meets the requested interval. The measured maximum delay is below 10 s in part because overdue work becomes a gap rather than a late observation; do not read the low delay as successful coverage.
- At 250, 12,353 slots are accounted for by gap rows and only 8 by observations versus ~15,000 expected over 10 minutes. Setup and stage-end alignment, pending work, and end-of-run accounting are not represented as completed checks. The 100 stage has 5,984 missing slots and 12 recorded against ~6,000; the small remainder similarly need not be healthy.
- This is one run per level on one workstation, with the sample fixture sharing the host and an already populated persistent DynamoDB Local volume. The [diagnosis above](#diagnosis-scheduler-dispatch-collapse) attributes the collapse to scheduler dispatch; DynamoDB Local latency lengthens each serial cycle but is not the ceiling. No load level was silently reduced or tuned. The design records raw completed observations and explicit scheduler gaps, not all possible unprocessed slots at shutdown.
- CPU and memory are sampled, not continuous maxima; percentiles depend on the 10-second probe cadence. Docker CPU and `/proc` RSS use different measurement mechanisms. The Overview and one monitor's 24 h summary represent API read latency, not all monitor summaries. During the database outage no GET latency is counted for failed requests.
- DynamoDB Local's process, TTL, transaction implementation, and limits differ from hosted DynamoDB. **Nothing here claims hosted DynamoDB capacity, throttling, recovery time, or pricing.** The demo table and non-capacity user tables were not used by the driver.
