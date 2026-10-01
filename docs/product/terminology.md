# Terminology

Shared vocabulary for code, documentation, and the interface. The words were chosen so that the product
never has to say "up" when it means "the last check we managed to run succeeded".

## Core objects

| Term | Meaning | Not to be confused with |
| --- | --- | --- |
| **Monitor** | A configured expectation about a target: what to request, what counts as meeting the expectation, how often, and whether it is active or paused. Configuration changes are versioned. | The target itself; an incident. |
| **Target** | The thing a monitor checks: an HTTP endpoint on an allow-listed loopback `host:port` in the local product. | Arbitrary internet hosts. |
| **Check** | One bounded attempt to evaluate a monitor's expectation against its target under explicit limits (deadline, body size, no redirects). | A scheduled unit of work (see *work item*). |
| **Observation** | The durable result of a completed check: what was requested, under which configuration version, when it was due and ran, what came back, and whether it met the expectation. | Inferred health of the whole application. |
| **Work item** | A durable record that a check is due, claimed, done, or missed. Carries the due instant, lease, and attempt count so duplicate, stale, or obsolete work can be recognised. | An observation (work may close without producing one). |
| **Gap** | A durable record of consecutive scheduled slots that were **not observed**, with a reason (`not_scheduled`, `overdue`, `interrupted`, `not_receiving`). Shown, never filled in. | A failed check (that is an observation). |
| **Incident** | A coherent period during which eligible evidence says a monitor is failing, from opening evidence through recovery. Repeated failures extend one incident. | Every individual failed observation. |
| **Notification intent** | A durable obligation to deliver a message about an event, independent of whether delivery has succeeded yet. | A delivery attempt. |
| **Delivery attempt** | One try to hand a notification to a receiver, with its outcome. Several attempts may serve one intent. | Proof a human read the message. |
| **Heartbeat** | A report that a scheduled job ran, received within an expected interval plus grace. Absence is a *missed heartbeat*. | Proof the job's output is correct or a backup is restorable. |
| **Deployment marker** | An append-only record that a release of an application was reported at a time. Context for incidents, never causation. | A trigger for health changes. |

## State dimensions

These are separate dimensions, not one enumeration. A monitor can be paused *and* have a last observation.

| State | Meaning |
| --- | --- |
| **Observed healthy** | An eligible check met the configured expectation. |
| **Observed failing** | An eligible check did not meet the expectation. |
| **Late** | A heartbeat's deadline passed and the report arrived inside the grace period, or is still being waited for inside it. |
| **Unknown / stale coverage** | No eligible evidence, or evidence older than the freshness limit. Never displayed as healthy. |
| **Paused** | Monitoring intentionally suspended; the last observation is retained but not presented as current coverage. |
| **Checker problem** | StatusForge could not complete or record a check reliably (policy refusal, interruption, store unavailable). Distinct from target failure. |
| **Delivery problem** | A notification could not be delivered as intended. Visible until resolved. |
| **Not receiving** | StatusForge itself was not running or not writing its liveness row; heartbeats could not have been received and HTTP checks could not have run. |

## Evidence words

| Term | Meaning |
| --- | --- |
| **Eligible evidence** | An observation allowed to change status or incident state: from the current configuration version, counted, not from a paused period, and not older than the state it would replace. |
| **Counted** | An observation that enters status, incidents, and summary denominators. Manual checks, checks under a superseded configuration, and checks from a paused period are recorded but not counted. |
| **Freshness** | Age of the newest counted observation relative to the monitor's interval (two intervals plus a margin). Drives the stale state. |
| **Coverage** | The number of scheduled slots recorded versus not observed in a window, reported with its denominator and gaps. Not "uptime". |
| **Coverage gap** | Slots with no observation (machine asleep, application stopped, workers busy, store unavailable). Shown, never filled in. |
| **Manual check** | A check initiated by the operator now. Labelled as manual; it never changes incident state. |
| **Controlled fixture** | A loopback-only target, receiver, or sample job whose behaviour the developer controls (healthy, failing, slow, drop, …). |

## Processing words

| Term | Meaning |
| --- | --- |
| **Target failure** | A completed check that observed the target failing (timeout, wrong status, refused connection). This is evidence and is recorded. |
| **Processing failure** | StatusForge failed before durably recording an outcome. Retrying is an infrastructure decision and must not rewrite an unfavourable observation. |
| **Duplicate-safe** | Applying the same effect twice yields the same durable state (conditional writes, deterministic keys, `Idempotency-Key`). |
| **Overdue work** | Work whose due instant has passed by more than one interval without being claimed. It is closed as a gap, never run late. |
| **Lease** | A time-bounded claim on a work item by one worker. An expired lease can be taken over; a stale holder's result is rejected. |
