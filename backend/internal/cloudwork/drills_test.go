package cloudwork_test

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/cloudwork"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// Happy path: declared monitor → planner pass → the sent message → worker.
func TestWorkerHappyPath(t *testing.T) {
	d := newDrill(t)
	d.declare(declare("api", 60, 1000))
	at := d.start("api", 60)
	report := d.pass(d.store())
	if report.Reconciled.Created != 1 || report.Dispatched != 1 || report.SendFailures != 0 {
		t.Fatalf("pass: %+v", report)
	}
	sent := d.queue.take()
	m := d.monitor("api")
	if want := message(m.ID, workStampOf(at)); !slices.Equal(sent, []string{want}) {
		t.Fatalf("sent %v, want the create run %s", sent, want)
	}
	response, counts := d.deliver(d.store(), sent...)
	if len(response.BatchItemFailures) != 0 || counts["recorded"] != 1 {
		t.Fatalf("delivery: failures %v counts %v", failed(response), counts)
	}
	obs := d.observations(m.ID)
	if len(obs) != 1 || !obs[0].Counted || obs[0].Outcome != "healthy" ||
		obs[0].InitiatedBy != "scheduled" || *obs[0].Trigger != "create" {
		t.Fatalf("observations: %+v", obs)
	}
	works := d.works(m.ID)
	if len(works) != 1 || works[0].State != "done" {
		t.Fatalf("work: %+v", works)
	}
	if lines := d.logs.lines("check"); len(lines) != 1 || lines[0]["monitor_id"] != m.ID {
		t.Fatalf("per-check line: %v", lines)
	}
	pass := d.logs.lines("planner pass")
	if len(pass) != 1 || pass[0]["dispatched"] != 1.0 || pass[0]["reconciled_created"] != 1.0 {
		t.Fatalf("planner pass line: %v", pass)
	}
	for _, line := range append(pass, d.logs.lines("check")...) {
		for _, v := range line {
			if s, ok := v.(string); ok && strings.Contains(s, "status.example.com") {
				t.Fatalf("log line carries a URL: %v", line)
			}
		}
	}
}

func workStampOf(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

// firstDelivery runs the happy-path setup and returns the monitor and its message.
func firstDelivery(
	t *testing.T,
	d *drill,
	key string,
	interval, deadline int,
) (monitor.Monitor, string) {
	t.Helper()
	d.declare(declare(key, interval, deadline))
	d.start(key, interval)
	d.pass(d.store())
	sent := d.queue.take()
	if len(sent) != 1 {
		t.Fatalf("sent %v", sent)
	}
	return d.monitor(key), sent[0]
}

// A duplicate delivered after the first is not_eligible and acknowledged.
func TestDuplicateSequential(t *testing.T) {
	d := newDrill(t)
	m, body := firstDelivery(t, d, "dup", 60, 1000)
	if r, counts := d.deliver(d.store(), body); len(r.BatchItemFailures) != 0 ||
		counts["recorded"] != 1 {
		t.Fatalf("first: %v %v", failed(r), counts)
	}
	r, counts := d.deliver(d.store(), body)
	if len(r.BatchItemFailures) != 0 || counts["not_eligible"] != 1 || counts["recorded"] != 0 {
		t.Fatalf("duplicate: %v %v", failed(r), counts)
	}
	if obs := d.observations(m.ID); len(obs) != 1 {
		t.Fatalf("observations: %d", len(obs))
	}
}

// barrierStore holds each handler after its work read until both have read,
// so both see the item pending and race on the claim.
type barrierStore struct {
	*store.Store
	barrier *sync.WaitGroup
}

func (b barrierStore) GetWork(ctx context.Context, id, dueAt string) (store.Work, error) {
	w, err := b.Store.GetWork(ctx, id, dueAt)
	b.barrier.Done()
	b.barrier.Wait()
	return w, err
}

// Two handlers, each in its own container, get the same message at once.
func TestDuplicateConcurrent(t *testing.T) {
	d := newDrill(t)
	m, body := firstDelivery(t, d, "race", 60, 1000)
	var barrier sync.WaitGroup
	barrier.Add(2)
	var wg sync.WaitGroup
	failures := make([][]string, 2)
	for i := range 2 {
		worker := cloudwork.Worker{
			Store: barrierStore{d.store(), &barrier}, Checker: d.checker,
			Now: d.clock.Now, Logger: d.logger,
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			response, err := worker.Handle(withRole(ctx, roleWorker), event(body))
			if err != nil {
				t.Error(err)
			}
			failures[i] = failed(response)
		})
	}
	wg.Wait()
	lines := d.logs.lines("worker batch")
	recorded, other := 0, 0
	for _, line := range lines {
		recorded += int(line["recorded"].(float64))
		other += int(line["lease_held"].(float64)) + int(line["not_eligible"].(float64))
	}
	for i := range 2 {
		if len(failures[i]) != 0 {
			t.Fatalf("handler %d reported failures %v; logs %v", i, failures[i], lines)
		}
	}
	if recorded != 1 || other != 1 {
		t.Fatalf("recorded %d, lease_held/not_eligible %d: %v", recorded, other, lines)
	}
	if obs := d.observations(m.ID); len(obs) != 1 || !obs[0].Counted {
		t.Fatalf("observations: %+v", obs)
	}
	if works := d.works(m.ID); works[0].Attempts != 1 || works[0].State != "done" {
		t.Fatalf("work claimed more than once: %+v", works)
	}
}

// crash claims the work as a worker would, then abandons it.
func (d *drill) crash(m monitor.Monitor, body string) {
	d.t.Helper()
	msg, err := cloudwork.ParseMessage(body)
	if err != nil {
		d.t.Fatal(err)
	}
	s := d.store()
	w, err := s.GetWork(d.t.Context(), msg.MonitorID, msg.DueAt)
	if err != nil {
		d.t.Fatal(err)
	}
	if _, token, err := s.Claim(d.t.Context(), w, d.clock.Now()); err != nil || token == "" {
		d.t.Fatalf("claim before crash: %q %v", token, err)
	}
}

// After a crash the planner re-dispatches the expired lease and attempt 2
// records; a second crash leaves one lease_expired gap.
func TestWorkerCrashAfterClaim(t *testing.T) {
	d := newDrill(t)
	m, body := firstDelivery(t, d, "crash", 60, 1000)
	d.crash(m, body)
	d.clock.Advance(5 * time.Second)
	if report := d.pass(d.store()); report.Dispatched != 0 {
		t.Fatalf("held lease re-dispatched: %+v", report)
	}
	d.clock.Advance(7 * time.Second) // past the 11 s lease, inside the interval
	d.pass(d.store())
	sent := d.queue.take()
	if !slices.Equal(sent, []string{body}) {
		t.Fatalf("re-dispatch: %v", sent)
	}
	if r, counts := d.deliver(d.store(), sent...); len(r.BatchItemFailures) != 0 ||
		counts["recorded"] != 1 {
		t.Fatalf("attempt 2: %v %v", failed(r), counts)
	}
	if obs := d.observations(m.ID); len(obs) != 1 || !obs[0].Counted {
		t.Fatalf("observations: %+v", obs)
	}
	if w := d.works(m.ID)[0]; w.State != "done" || w.Attempts != 2 {
		t.Fatalf("work after attempt 2: %+v", w)
	}

	// The next slot: crash on attempt 1 and again on attempt 2.
	d.clock.Advance(60 * time.Second)
	d.pass(d.store())
	sent = d.queue.take()
	if len(sent) != 1 {
		t.Fatalf("next slot: %v", sent)
	}
	d.crash(m, sent[0])
	d.clock.Advance(12 * time.Second)
	d.pass(d.store())
	again := d.queue.take()
	if !slices.Equal(again, sent) {
		t.Fatalf("second re-dispatch: %v", again)
	}
	d.crash(m, again[0])
	d.clock.Advance(12 * time.Second)
	report := d.pass(d.store())
	if report.Dispatched != 0 || report.Expired != 1 {
		t.Fatalf("after second crash: %+v", report)
	}
	gaps := d.gaps(m.ID)
	if len(gaps) != 1 || gaps[0].Reason != "lease_expired" || gaps[0].MissedCount != 1 {
		t.Fatalf("gaps: %+v", gaps)
	}
	if len(d.observations(m.ID)) != 1 {
		t.Fatal("an abandoned attempt stored an observation")
	}
}

// A failed send leaves the row pending; the next pass sends it.
func TestPlannerSendFailure(t *testing.T) {
	d := newDrill(t)
	d.declare(declare("send", 300, 1000))
	d.start("send", 300)
	d.queue.failNext(1)
	report := d.pass(d.store())
	if report.Dispatched != 0 || report.SendFailures != 1 {
		t.Fatalf("failed pass: %+v", report)
	}
	m := d.monitor("send")
	if w := d.works(m.ID); len(w) != 1 || w[0].State != "pending" {
		t.Fatalf("work after failed send: %+v", w)
	}
	d.clock.Advance(60 * time.Second)
	if report := d.pass(d.store()); report.Dispatched != 1 || report.SendFailures != 0 {
		t.Fatalf("retry pass: %+v", report)
	}
	sent := d.queue.take()
	if r, counts := d.deliver(d.store(), sent...); len(r.BatchItemFailures) != 0 ||
		counts["recorded"] != 1 {
		t.Fatalf("delivery: %v %v", failed(r), counts)
	}
	if obs := d.observations(m.ID); len(obs) != 1 {
		t.Fatalf("observations: %d", len(obs))
	}
}

// A message that is never consumed becomes one overdue gap.
func TestNeverConsumed(t *testing.T) {
	d := newDrill(t)
	m, body := firstDelivery(t, d, "lost", 60, 1000)
	msg, _ := cloudwork.ParseMessage(body)
	d.clock.Advance(59 * time.Second)
	if report := d.pass(d.store()); report.Expired != 0 {
		t.Fatalf("closed before dueAt + interval: %+v", report)
	}
	d.queue.take()
	d.clock.Advance(time.Second)
	report := d.pass(d.store())
	if report.Expired != 1 {
		t.Fatalf("pass at dueAt + interval: %+v", report)
	}
	gaps := d.gaps(m.ID)
	if len(gaps) != 1 || gaps[0].Reason != "overdue" || gaps[0].FromDueAt != msg.DueAt ||
		gaps[0].ToDueAt != msg.DueAt || gaps[0].MissedCount != 1 {
		t.Fatalf("gaps: %+v", gaps)
	}
}

// downtime runs drill 7's scenario; cold selects a fresh store per call.
func downtime(t *testing.T, cold bool) {
	d := newDrill(t)
	shared := d.store()
	get := func() *store.Store {
		if cold {
			return d.store()
		}
		return shared
	}
	d.declare(declare("down", 60, 1000))
	d.start("down", 60)
	d.pass(get())
	if r, counts := d.deliver(get(), d.queue.take()...); len(r.BatchItemFailures) != 0 ||
		counts["recorded"] != 1 {
		t.Fatalf("first slot: %v %v", failed(r), counts)
	}
	m := d.monitor("down")
	d.clock.Advance(4 * 60 * time.Second) // three passes missed
	report := d.pass(get())
	if report.WorkCreated != 1 || report.GapsCreated != 1 || report.Dispatched != 1 {
		t.Fatalf("catch-up pass: %+v", report)
	}
	sent := d.queue.take()
	if r, counts := d.deliver(get(), sent...); len(r.BatchItemFailures) != 0 ||
		counts["recorded"] != 1 {
		t.Fatalf("catch-up slot: %v %v", failed(r), counts)
	}
	// A repeated pass at the same time, cold or warm, adds nothing.
	if again := d.pass(get()); again.WorkCreated != 0 || again.GapsCreated != 0 ||
		again.Dispatched != 0 {
		t.Fatalf("repeated pass: %+v", again)
	}
	gaps := d.gaps(m.ID)
	if len(gaps) != 1 || gaps[0].Reason != "not_scheduled" || gaps[0].MissedCount != 3 {
		t.Fatalf("gaps: %+v", gaps)
	}
	works := d.works(m.ID)
	if len(works) != 2 || works[0].Trigger != "create" || works[1].Trigger != "schedule" ||
		works[1].State != "done" {
		t.Fatalf("works: %+v", works)
	}
	step := time.Minute
	first, _ := time.Parse(time.RFC3339Nano, gaps[0].FromDueAt)
	last, _ := time.Parse(time.RFC3339Nano, gaps[0].ToDueAt)
	catchUp, _ := time.Parse(time.RFC3339Nano, works[1].DueAt)
	if last.Sub(first) != 2*step || catchUp.Sub(last) != step {
		t.Fatalf("gap %s–%s, catch-up %s", gaps[0].FromDueAt, gaps[0].ToDueAt, works[1].DueAt)
	}
	if obs := d.observations(m.ID); len(obs) != 2 {
		t.Fatalf("observations: %d", len(obs))
	}
}

// Planner downtime of three intervals.
func TestPlannerDowntime(t *testing.T) { downtime(t, false) }

// The same with a new Store for every pass and every delivery (cold starts).
func TestColdStart(t *testing.T) { downtime(t, true) }

// An edited declaration bumps the version and queues a config_change run;
// old-version messages never record a counted result.
func TestConfigurationChange(t *testing.T) {
	d := newDrill(t)
	m, body := firstDelivery(t, d, "edit", 60, 1000)
	d.deliver(d.store(), body)
	d.clock.Advance(60 * time.Second)
	d.pass(d.store())
	old := d.queue.take()
	if len(old) != 1 {
		t.Fatalf("slot message: %v", old)
	}
	// The declaration changes before the slot's message is consumed.
	d.declare(declare("edit", 60, 2000))
	d.clock.Advance(5 * time.Second)
	report := d.pass(d.store())
	if report.Reconciled.Updated != 1 || report.Dispatched != 1 {
		t.Fatalf("edit pass: %+v", report)
	}
	changed := d.monitor("edit")
	if changed.ConfigVersion != 2 || changed.Check.DeadlineMs != 2000 {
		t.Fatalf("monitor after edit: %+v", changed)
	}
	run := d.queue.take()
	if r, counts := d.deliver(d.store(), old...); len(r.BatchItemFailures) != 0 ||
		counts["not_eligible"] != 1 {
		t.Fatalf("old-version message: %v %v", failed(r), counts)
	}
	if r, counts := d.deliver(d.store(), run...); len(r.BatchItemFailures) != 0 ||
		counts["recorded"] != 1 {
		t.Fatalf("config_change run: %v %v", failed(r), counts)
	}
	works := d.works(m.ID)
	if works[len(works)-1].Trigger != "config_change" || works[len(works)-1].ConfigVersion != 2 {
		t.Fatalf("works: %+v", works)
	}

	// In flight: the declaration changes between claim and record.
	d.clock.Advance(60 * time.Second)
	d.pass(d.store())
	inflight := d.queue.take()
	d.declare(declare("edit", 60, 3000))
	d.checker.during = func() { d.pass(d.store()) }
	if r, counts := d.deliver(d.store(), inflight...); len(r.BatchItemFailures) != 0 ||
		counts["recorded"] != 1 {
		t.Fatalf("in-flight: %v %v", failed(r), counts)
	}
	obs := d.observations(m.ID)
	latest := obs[0]
	if latest.Counted || latest.NotCountedReason == nil ||
		*latest.NotCountedReason != monitor.NotCountedConfigChanged || latest.ConfigVersion != 2 {
		t.Fatalf("in-flight observation: %+v", latest)
	}
	for _, o := range obs {
		if o.Counted && o.ConfigVersion == 1 && o.DueAt != nil &&
			*o.DueAt != mustMessage(t, body).DueAt {
			t.Fatalf("old version counted: %+v", o)
		}
	}
	if d.monitor("edit").ConfigVersion != 3 {
		t.Fatal("in-flight edit not saved")
	}
}

func mustMessage(t *testing.T, body string) cloudwork.Message {
	m, err := cloudwork.ParseMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A removed declaration archives the monitor; its pending message is
// acknowledged as not_eligible.
func TestDeclarationRemoved(t *testing.T) {
	d := newDrill(t)
	m, body := firstDelivery(t, d, "gone", 60, 1000)
	d.declare()
	d.clock.Advance(5 * time.Second)
	if report := d.pass(d.store()); report.Reconciled.Archived != 1 || report.Dispatched != 0 {
		t.Fatalf("removal pass: %+v", report)
	}
	if got := d.monitor("gone"); got.Lifecycle != "archived" || got.ID != m.ID {
		t.Fatalf("monitor: %+v", got)
	}
	r, counts := d.deliver(d.store(), body)
	if len(r.BatchItemFailures) != 0 || counts["not_eligible"] != 1 {
		t.Fatalf("pending message: %v %v", failed(r), counts)
	}
	if len(d.observations(m.ID)) != 0 {
		t.Fatal("archived monitor recorded an observation")
	}
	// Unchanged declarations write nothing.
	if report := d.pass(d.store()); report.Reconciled != (cloudwork.ReconcileReport{}) {
		t.Fatalf("idempotent pass: %+v", report)
	}
}

// Failing twice then recovering twice opens and resolves an incident with
// cancelled/no_channel intents, no DELIVERY pointer, and a retention step
// stamps the resolved incident.
func TestIncidentFlowNoChannel(t *testing.T) {
	d := newDrill(t)
	d.checker.outcomes = []string{"failing", "failing", "healthy", "healthy"}
	m, body := firstDelivery(t, d, "inc", 60, 1000)
	bodies := []string{body}
	for i := range 4 {
		if i > 0 {
			d.clock.Advance(60 * time.Second)
			d.pass(d.store())
			bodies = d.queue.take()
		}
		r, counts := d.deliver(d.store(), bodies...)
		if len(r.BatchItemFailures) != 0 || counts["recorded"] != 1 {
			t.Fatalf("delivery %d: %v %v", i, failed(r), counts)
		}
	}
	incidents := d.query("MON#"+m.ID, "INC#")
	if len(incidents) != 1 || str(incidents[0], "state") != "resolved" {
		t.Fatalf("incidents: %v", incidents)
	}
	id := str(incidents[0], "incidentId")
	notes := d.query("MON#"+m.ID, "INCX#"+id+"#NOTE#")
	if len(notes) != 2 {
		t.Fatalf("intents: %d", len(notes))
	}
	for _, n := range notes {
		if str(n, "state") != "cancelled" ||
			str(n, "cancelledReason") != store.CancelledNoChannel ||
			n["nextAttemptAt"] != nil {
			t.Fatalf("intent: %v", n)
		}
	}
	for _, prefix := range []string{"DUE#", "ATTENTION#"} {
		if pointers := d.query("DELIVERY", prefix); len(pointers) != 0 {
			t.Fatalf("DELIVERY %s pointers: %d", prefix, len(pointers))
		}
	}
	if incidents[0]["expiresAt"] != nil {
		t.Fatal("stamped before a retention step")
	}
	d.clock.Advance(10 * time.Second)
	if report := d.pass(d.store()); report.RetentionSteps < 1 {
		t.Fatalf("retention: %+v", report)
	}
	incidents = d.query("MON#"+m.ID, "INC#")
	if incidents[0]["expiresAt"] == nil {
		t.Fatal("resolved incident not stamped")
	}
	if jobs := d.query("JOBS", "JOB#retain#"); len(jobs) != 0 {
		t.Fatalf("retention job left: %d", len(jobs))
	}
	detail, _, _, _, err := d.store().IncidentDetail(t.Context(), m.ID, id, d.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if detail.NotificationSummary != (store.Summary{}) {
		t.Fatalf("summary counts no_channel intents: %+v", detail.NotificationSummary)
	}
}

// Bad JSON and v:2 are reported; an unknown monitor is missing.
func TestInvalidMessages(t *testing.T) {
	d := newDrill(t)
	stamp := workStampOf(time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC))
	r, counts := d.deliver(
		d.store(),
		`{"v":1,"monitorId":`,
		`{"v":2,"monitorId":"ABC","dueAt":"`+stamp+`"}`,
		message("UNKNOWNMONITOR", stamp),
	)
	if got := failed(r); !slices.Equal(got, []string{"m0", "m1"}) {
		t.Fatalf("failures: %v", got)
	}
	if counts["invalid_message"] != 2 || counts["missing"] != 1 {
		t.Fatalf("counts: %v", counts)
	}
	for _, bad := range []string{
		`{"v":1,"monitorId":"A","dueAt":"2026-09-29T10:00:00Z"}`,
		`{"v":1,"monitorId":"A#B","dueAt":"` + stamp + `"}`,
		`{"v":1,"monitorId":"A","dueAt":"` + stamp + `","extra":1}`,
		`{"v":1,"monitorId":"A","dueAt":"` + stamp + `"} {}`,
	} {
		if _, err := cloudwork.ParseMessage(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// With less than deadlineMs + 15 s left, records are reported without being
// claimed.
func TestRemainingTimeShort(t *testing.T) {
	d := newDrill(t)
	d.declare(declare("short-a", 60, 5000), declare("short-b", 60, 5000))
	d.start("short-a", 60)
	d.pass(d.store())
	sent := d.queue.take()
	if len(sent) != 2 {
		t.Fatalf("sent: %v", sent)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 19*time.Second) // < 5 s + 15 s
	defer cancel()
	r, counts := d.deliverCtx(ctx, d.store(), sent...)
	if got := failed(r); !slices.Equal(got, []string{"m0", "m1"}) {
		t.Fatalf("failures: %v", got)
	}
	if counts["deferred"] != 2 || d.checker.callCount() != 0 {
		t.Fatalf("counts %v, checks %d", counts, d.checker.callCount())
	}
	for _, key := range []string{"short-a", "short-b"} {
		for _, w := range d.works(d.monitor(key).ID) {
			if w.State != "pending" || w.Attempts != 0 {
				t.Fatalf("claimed despite short time: %+v", w)
			}
		}
	}
	// With enough time the same messages record.
	if r, counts := d.deliver(d.store(), sent...); len(r.BatchItemFailures) != 0 ||
		counts["recorded"] != 2 {
		t.Fatalf("full-time delivery: %v %v", failed(r), counts)
	}
}

// The declaration parser names the entry and field, never the value.
func TestDeclarationErrors(t *testing.T) {
	d := newDrill(t)
	cases := map[string]string{
		`{}`:            "STATUSFORGE_CLOUD_MONITORS: must be a JSON array of monitor declarations",
		`[{"key":"a"}]`: "STATUSFORGE_CLOUD_MONITORS[0].name: required",
		`[` + declare("ok", 60, 1000) + `,{"key":"Bad","name":"n","url":"https://x","intervalSeconds":60}]`: "STATUSFORGE_CLOUD_MONITORS[1].key: must be 1–64 characters of [a-z0-9-]",
		`[{"key":"a","name":"n","url":"http://status.example.com/","intervalSeconds":60}]`:                  "STATUSFORGE_CLOUD_MONITORS[0].url: scheme_not_allowed",
		`[{"key":"a","name":"n","url":"https://secret.example.net/","intervalSeconds":60}]`:                 "STATUSFORGE_CLOUD_MONITORS[0].url: target_not_allowed",
		`[{"key":"a","name":"n","url":"https://status.example.com/","intervalSeconds":10}]`:                 "STATUSFORGE_CLOUD_MONITORS[0].intervalSeconds: invalid_value",
		`[{"key":"a","name":"n","url":"https://status.example.com/","intervalSeconds":60,"deadlineMs":50}]`: "STATUSFORGE_CLOUD_MONITORS[0].deadlineMs: out_of_range",
		`[{"key":"a","name":"n","url":"https://status.example.com/","intervalSeconds":60,"extra":true}]`:    "STATUSFORGE_CLOUD_MONITORS[0].entry: must be an object with known fields only",
		`[` + declare("a", 60, 1000) + `,` + declare("a", 120, 1000) + `]`:                                  "STATUSFORGE_CLOUD_MONITORS[1].key: duplicate",
	}
	for raw, want := range cases {
		_, err := cloudwork.ParseDeclarations(raw, d.policy)
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %s", raw, err, want)
		}
	}
	// A pass with an invalid declaration fails before any write.
	d.monitors = `[{"key":"a","name":"n","url":"https://secret.example.net/","intervalSeconds":60}]`
	d.start("a", 60)
	if _, err := d.passErr(d.store()); err == nil {
		t.Fatal("invalid declaration accepted")
	}
	// Resolution at save refuses a private address before any write.
	d.policy.SetResolver(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.9")}}, nil
	})
	d.declare(declare("a", 60, 1000))
	if _, err := d.passErr(d.store()); err == nil ||
		err.Error() != "STATUSFORGE_CLOUD_MONITORS[0].url: refused_by_policy" {
		t.Fatalf("private resolution: %v", err)
	}
	ms, err := d.store().List(t.Context())
	if err != nil || len(ms) != 0 {
		t.Fatalf("writes after a failed pass: %d %v", len(ms), err)
	}
}
