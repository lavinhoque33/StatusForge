package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/config"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

func main() {
	if err := seed(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// service is one seeded HTTP monitor and the plan that decides its synthetic checks.
type service struct {
	monitor.Monitor
	plan    func(age, every time.Duration) result
	active  bool
	healthy int       // consecutive healthy checks
	last    time.Time // due time of the latest check
}

// demo drives the HTTP monitors through the live store's write paths with a simulated clock.
type demo struct {
	ctx   context.Context
	s     *store.Store
	now   time.Time
	scene time.Time // start of the synthetic week
	end   time.Time // seed start: history stops here
	url   string    // base URL of the first allowed target
	app   store.Application
	step  time.Duration // current interval between checks
	// reminder is the interval between reminders for an open incident.
	reminder time.Duration

	recoveries, incident, curve, worker *service
	deployed                            bool
}

func seed(ctx context.Context) error {
	seedStarted := time.Now()
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	if cfg.DynamoDBTable != "statusforge_demo" {
		return fmt.Errorf(
			"demo refuses table %q: only statusforge_demo is permitted",
			cfg.DynamoDBTable,
		)
	}
	if reset := os.Getenv("DEMO_RESET"); reset != "" && reset != "yes" {
		return errors.New("DEMO_RESET must be yes when set")
	}
	client := localdynamo.New(
		cfg.DynamoDBEndpoint,
		"127.0.0.1",
		cfg.DynamoDBRegion,
		cfg.DynamoDBAccessKeyID,
		cfg.DynamoDBSecretAccessKey,
	)
	if err := store.PrepareDemo(ctx, client, cfg.DynamoDBTable, os.Getenv("DEMO_RESET") == "yes"); err != nil {
		return err
	}
	// Each seeding thread owns a store with its own simulated clock.
	newStore := func(clock *time.Time) *store.Store {
		return store.New(
			client.DynamoDB(),
			cfg.DynamoDBTable,
			cfg.ReadinessTimeout,
			func() time.Time { return *clock },
		)
	}
	realNow := time.Now().UTC().Truncate(time.Second)
	d := &demo{
		scene:    realNow.Add(-7*day + time.Minute),
		end:      realNow,
		url:      "http://" + strings.TrimSpace(strings.Split(cfg.AllowedTargets, ",")[0]),
		step:     interval,
		reminder: time.Duration(cfg.ReminderIntervalSeconds) * time.Second,
	}
	d.now = d.scene
	d.s = newStore(&d.now)
	d.s.SetReminderInterval(d.reminder)
	if err := d.s.Initialize(ctx); err != nil {
		return err
	}
	if d.app, _, err = d.s.CreateApplication(ctx, "Demo application", d.scene); err != nil {
		return err
	}
	token := os.Getenv("STATUSFORGE_SAMPLE_JOB_TOKEN")
	if token == "" {
		if token, err = heartbeat.Generate(); err != nil {
			return err
		}
	}
	// The heartbeat shares no records with the HTTP monitors except the
	// application, so its week is seeded concurrently to bound the seed time.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.ctx = ctx
	pulse := &heartbeatWeek{ctx: ctx, scene: d.scene, end: d.end, app: d.app.ID, token: token}
	pulse.s = newStore(&pulse.now)
	done := make(chan error, 1)
	go func() { done <- pulse.run() }()
	err = d.httpWeek()
	if err != nil {
		cancel()
	}
	if err := errors.Join(err, <-done); err != nil {
		return err
	}
	d.now = d.end
	if _, err := d.s.WriteLiveness(ctx, d.now, time.Hour); err != nil {
		return err
	}
	incidents, err := d.verify(pulse.monitor)
	if err != nil {
		return err
	}
	if err := d.s.MarkDemo(ctx); err != nil {
		return err
	}
	fmt.Printf(
		"demo seeded table=statusforge_demo heartbeat=%s monitors=%d incidents=%d seed_seconds=%.2f (open and resolved), 7-day summaries verified\n",
		pulse.monitor.ID,
		len(d.services())+1,
		incidents,
		time.Since(seedStarted).Seconds(),
	)
	return nil
}

func (d *demo) services() []*service {
	return []*service{d.recoveries, d.incident, d.curve, d.worker}
}

// create stores a monitor at the current clock, joins it to the application and
// records its creation check as a worker would.
func (d *demo) create(
	name string,
	p profile,
	plan func(age, every time.Duration) result,
) (*service, error) {
	m := monitor.New(name, monitor.Check{
		URL:            d.url + "/healthy",
		Method:         "GET",
		ExpectedStatus: 200,
		DeadlineMs:     int(p.deadline),
		MaxBodyBytes:   monitor.MaxBodyBytes,
	}, d.now)
	m.IntervalSeconds = int(d.step / time.Second)
	if err := d.s.Create(d.ctx, m); err != nil {
		return nil, err
	}
	if _, err := d.s.SetApplication(d.ctx, m.ID, d.app.ID); err != nil {
		return nil, err
	}
	svc := &service{Monitor: m, plan: plan, active: true}
	return svc, d.run(svc, d.now)
}

func (d *demo) lifecycle(svc *service, action string) error {
	if _, err := d.s.Lifecycle(d.ctx, svc.ID, action, d.now); err != nil {
		return fmt.Errorf("demo %s %s: %w", action, svc.Name, err)
	}
	svc.active = action == "resume"
	if svc.active {
		return d.run(svc, d.now) // the resume check
	}
	return nil
}

// tighten moves every active monitor to the final interval.
func (d *demo) tighten() error {
	d.step = finalInterval
	every := int(finalInterval / time.Second)
	for _, svc := range d.services() {
		if svc == nil || !svc.active {
			continue
		}
		current, err := d.s.Get(d.ctx, svc.ID)
		if err != nil {
			return err
		}
		if _, err := d.s.PatchInterval(
			d.ctx, svc.ID, current.ConfigVersion, nil, nil, &every, d.now,
		); err != nil {
			return fmt.Errorf("demo interval %s: %w", svc.Name, err)
		}
		svc.IntervalSeconds = every
	}
	return nil
}

// deploy reports demo-2 and points the current-incident monitor at a failing endpoint.
func (d *demo) deploy() error {
	if _, err := d.s.PutDeployment(d.ctx, d.app.ID, "", store.Marker{
		Version: "demo-2", Source: "ingest", ReportedAt: monitor.Stamp(d.now),
	}); err != nil {
		return err
	}
	current, err := d.s.Get(d.ctx, d.incident.ID)
	if err != nil {
		return err
	}
	failing := current.Check
	failing.URL = d.url + "/failing"
	if _, err := d.s.Patch(d.ctx, d.incident.ID, current.ConfigVersion, nil, &failing, d.now); err != nil {
		return err
	}
	d.deployed = true
	return d.run(d.incident, d.now) // the configuration-change check
}

// httpWeek seeds the HTTP monitors with one deliberate receive gap. Story events
// take effect at the first interval boundary at or after their time.
func (d *demo) httpWeek() error {
	var err error
	if d.recoveries, err = d.create(
		"Demo service · recoveries and slow responses", recoveriesProfile, recoveriesCheck,
	); err != nil {
		return err
	}
	if d.incident, err = d.create(
		"Demo service · current incident", incidentProfile,
		func(age, every time.Duration) result { return currentIncidentCheck(age, every, d.deployed) },
	); err != nil {
		return err
	}
	if d.curve, err = d.create(
		"Demo API · daily load, timeouts and a pause", curveProfile, curveCheck,
	); err != nil {
		return err
	}
	if _, err := d.s.PutDeployment(d.ctx, d.app.ID, "", store.Marker{
		Version: "demo-1", Source: "ingest", ReportedAt: monitor.Stamp(d.scene.Add(day)),
	}); err != nil {
		return err
	}
	events := []struct {
		at   time.Duration
		fire func() error
	}{
		{maintenanceAt, func() error {
			_, err := d.s.CreateMaintenance(d.ctx, d.recoveries.ID, d.now.Add(interval),
				d.now.Add(2*time.Hour), "Demo planned maintenance", d.now)
			return err
		}},
		{workerCreatedAt, func() error {
			var err error
			d.worker, err = d.create("Demo worker · paused", workerProfile, workerCheck)
			return err
		}},
		{workerPausedAt, func() error { return d.lifecycle(d.worker, "pause") }},
		{tightenAt, d.tighten},
		{deployAt, d.deploy},
		{curvePausedAt, func() error { return d.lifecycle(d.curve, "pause") }},
		{curveResumedAt, func() error { return d.lifecycle(d.curve, "resume") }},
	}
	// Each window is as long as the monitors' interval, so it holds exactly one slot per monitor.
	for base := d.scene.Truncate(interval); base.Before(d.end); base = base.Add(d.step) {
		for len(events) > 0 && !base.Before(d.scene.Add(events[0].at)) {
			d.now = base
			if err := events[0].fire(); err != nil {
				return err
			}
			events = events[1:]
		}
		age := base.Sub(d.scene)
		if age >= receiveGapAt && age < receiveGapEnd {
			continue // an asleep machine: the next tick records the missed slots
		}
		type step struct {
			svc *service
			at  time.Time
		}
		var steps []step
		for _, svc := range d.services() {
			if svc == nil || !svc.active {
				continue
			}
			slot := monitor.GridSlot(svc.ID, svc.IntervalSeconds, base.Add(d.step-time.Second))
			// Skip a slot already covered by a creation, resume or configuration check,
			// and never record a check after seed start.
			if slot.After(svc.last) && slot.Add(5*time.Second).Before(d.end) {
				steps = append(steps, step{svc, slot})
			}
		}
		if len(steps) == 0 {
			continue
		}
		slices.SortFunc(steps, func(a, b step) int { return a.at.Compare(b.at) })
		// One scheduler pass after the window's last slot creates every slot's work.
		d.now = steps[len(steps)-1].at.Add(time.Second)
		report, err := d.s.TickCycle(d.ctx, d.now)
		if err == nil && report.Failed > 0 {
			err = store.ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("demo tick at %s: %w", d.now, err)
		}
		for _, st := range steps {
			if err := d.check(st.svc, st.at, report.Open[st.svc.ID]); err != nil {
				return err
			}
		}
	}
	if len(events) > 0 {
		return fmt.Errorf("demo week ended before %d story events", len(events))
	}
	return nil
}

// run performs the check of work created outside a scheduler pass.
func (d *demo) run(svc *service, at time.Time) error {
	works, err := d.s.Works(d.ctx, svc.Monitor, at.Add(time.Second))
	if err != nil {
		return err
	}
	return d.check(svc, at, works)
}

// check claims the newest pending work for svc at its due time, records the
// planned result, runs the reminder pass and delivers any notification the
// transition produced. Notifications follow only failures and the recovery checks
// after them, so both passes run only until a monitor has been healthy for three checks.
func (d *demo) check(svc *service, at time.Time, works []store.Work) error {
	every := time.Duration(svc.IntervalSeconds) * time.Second
	r := svc.plan(at.Sub(d.scene), every)
	svc.last = at
	if err := d.record(svc, at, r, works); err != nil {
		return fmt.Errorf("demo check %s at %s: %w", svc.Name, at, err)
	}
	if r.outcome != "healthy" {
		svc.healthy = 0
	} else if svc.healthy++; svc.healthy > 3 {
		return nil
	}
	if err := d.remind(svc); err != nil {
		return fmt.Errorf("demo reminder %s at %s: %w", svc.Name, d.now, err)
	}
	if err := d.deliver(); err != nil {
		return fmt.Errorf("demo delivery at %s: %w", d.now, err)
	}
	return nil
}

// remind creates a reminder for an open incident once its reminder is due, as
// the scheduler's maintenance pass does for every active monitor.
func (d *demo) remind(svc *service) error {
	current, err := d.s.Get(d.ctx, svc.ID)
	if err != nil || current.OpenIncident == nil {
		return err
	}
	if err := d.s.Reminder(d.ctx, current, d.now, d.reminder); err != nil &&
		!errors.Is(err, store.ErrNotEligible) {
		return err
	}
	return nil
}

func (d *demo) record(svc *service, at time.Time, r result, works []store.Work) error {
	d.now = at.Add(time.Second)
	var selected *store.Work
	for i := range works {
		if works[i].State == "pending" && (selected == nil || works[i].DueAt > selected.DueAt) {
			selected = &works[i]
		}
	}
	if selected == nil {
		return fmt.Errorf("no pending work at %s", d.now)
	}
	claimed, token, err := d.s.Claim(d.ctx, *selected, d.now)
	if err != nil {
		return err
	}
	started := d.now
	completed := started.Add(time.Duration(r.duration) * time.Millisecond)
	d.now = completed
	var status *int
	if r.status != 0 {
		status = &r.status
	}
	due := selected.DueAt
	_, err = d.s.RecordResult(
		d.ctx,
		monitor.Observation{
			ID:             fmt.Sprintf("demo-%s-%d", svc.ID, started.UnixNano()),
			MonitorID:      svc.ID,
			ConfigVersion:  claimed.ConfigVersion,
			InitiatedBy:    "scheduler",
			Trigger:        &selected.Trigger,
			DueAt:          &due,
			Request:        claimed.Check,
			StartedAt:      monitor.Stamp(started),
			CompletedAt:    monitor.Stamp(completed),
			DurationMs:     r.duration,
			Outcome:        r.outcome,
			Reason:         r.reason,
			ObservedStatus: status,
		},
		token,
	)
	return err
}

// deliver sends each notification when its transition happens, as the live
// worker would. The receiver rejects the recovery notice of the resolved
// incident, leaving a failed notification that can be retried against the live receiver.
func (d *demo) deliver() error {
	dues, err := d.s.Due(d.ctx, d.now)
	if err != nil {
		return err
	}
	for _, due := range dues {
		n, a, claim, err := d.s.ClaimDelivery(d.ctx, due, d.now)
		if errors.Is(err, store.ErrNotEligible) {
			continue
		}
		if err != nil {
			return err
		}
		result, status := "delivered", 204
		if due.MonitorID == d.recoveries.ID && n.Kind == "resolved" {
			result, status = "rejected", 400
		}
		if _, err := d.s.CompleteDelivery(d.ctx, due, n, a, claim, result, &status, 1, d.now, nil); err != nil {
			return err
		}
	}
	return nil
}
