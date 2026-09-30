package main

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	realNow := time.Now().UTC().Truncate(time.Second)
	scene := realNow.Add(-7*24*time.Hour + time.Minute)
	now := scene
	s := store.New(
		client.DynamoDB(),
		cfg.DynamoDBTable,
		cfg.ReadinessTimeout,
		func() time.Time { return now },
	)
	if err := s.Initialize(ctx); err != nil {
		return err
	}
	app, _, err := s.CreateApplication(ctx, "Demo application", scene)
	if err != nil {
		return err
	}
	check := monitor.Check{
		URL: "http://" + strings.TrimSpace(
			strings.Split(cfg.AllowedTargets, ",")[0],
		) + "/healthy",
		Method:         "GET",
		ExpectedStatus: 200,
		DeadlineMs:     3000,
		MaxBodyBytes:   monitor.MaxBodyBytes,
	}
	healthy := monitor.New("Demo service · recoveries and slow responses", check, scene)
	healthy.IntervalSeconds = 900
	failing := monitor.New("Demo service · current incident", check, scene)
	failing.IntervalSeconds = 900
	for _, m := range []monitor.Monitor{healthy, failing} {
		if err := s.Create(ctx, m); err != nil {
			return err
		}
		if _, err := s.SetApplication(ctx, m.ID, app.ID); err != nil {
			return err
		}
	}
	// All synthetic events pass through the live store's scheduling and receipt
	// transitions. At 15-minute intervals, the scene stays dense without
	// pretending that one request was made per minute for seven days.
	if _, err := s.PutDeployment(ctx, app.ID, "", store.Marker{
		Version: "demo-1", Source: "ingest", ReportedAt: monitor.Stamp(scene.Add(24 * time.Hour)),
	}); err != nil {
		return err
	}
	record := func(id string, at time.Time, outcome string, duration int64) error {
		m, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		now = at.Add(time.Second)
		works, err := s.Works(ctx, m, now)
		if err != nil {
			return err
		}
		var selected *store.Work
		for i := range works {
			if works[i].State == "pending" && (selected == nil || works[i].DueAt > selected.DueAt) {
				selected = &works[i]
			}
		}
		if selected == nil {
			return fmt.Errorf("no pending work for %s at %s", id, now)
		}
		claimed, token, err := s.Claim(ctx, *selected, now)
		if err != nil {
			return err
		}
		started := now
		completed := now.Add(time.Duration(duration) * time.Millisecond)
		now = completed
		reason := "ok"
		code := 200
		if outcome == "failing" {
			reason = "unexpected_status"
			code = 500
		}
		due := selected.DueAt
		_, err = s.RecordResult(
			ctx,
			monitor.Observation{
				ID:             fmt.Sprintf("demo-%s-%d", id, started.UnixNano()),
				MonitorID:      id,
				ConfigVersion:  claimed.ConfigVersion,
				InitiatedBy:    "scheduler",
				Trigger:        &selected.Trigger,
				DueAt:          &due,
				Request:        claimed.Check,
				StartedAt:      monitor.Stamp(started),
				CompletedAt:    monitor.Stamp(completed),
				DurationMs:     duration,
				Outcome:        outcome,
				Reason:         reason,
				ObservedStatus: &code,
			},
			token,
		)
		return err
	}
	// Deliver each notification when its transition happens, as the live worker would. One receiver
	// rejection leaves a failed notification that can be retried against the live receiver.
	deliver := func() error {
		dues, err := s.Due(ctx, now)
		if err != nil {
			return err
		}
		for _, d := range dues {
			n, a, claim, err := s.ClaimDelivery(ctx, d, now)
			if errors.Is(err, store.ErrNotEligible) {
				continue
			}
			if err != nil {
				return err
			}
			result, status := "delivered", 204
			if d.MonitorID == healthy.ID && n.Kind == "resolved" {
				result, status = "rejected", 400
			}
			if _, err := s.CompleteDelivery(ctx, d, n, a, claim, result, &status, 1, now, nil); err != nil {
				return err
			}
		}
		return nil
	}
	interval := 15 * time.Minute
	first := scene.Truncate(interval).Add(interval)
	// History ends at seed start; the live API then truthfully reports the seeding time as not receiving.
	last := realNow
	gapStart := scene.Add(3*24*time.Hour + 6*time.Hour)
	gapEnd := gapStart.Add(3 * time.Hour)
	maintenanceAt := scene.Add(4 * 24 * time.Hour)
	deployAt := scene.Add(6 * 24 * time.Hour)
	maintenanceCreated, deployed := false, false
	for base := first; base.Before(last); base = base.Add(interval) {
		if !maintenanceCreated && !base.Before(maintenanceAt) {
			now = base
			if _, err := s.CreateMaintenance(ctx, healthy.ID, base.Add(interval), base.Add(2*time.Hour),
				"Demo planned maintenance", now); err != nil {
				return err
			}
			maintenanceCreated = true
		}
		if !deployed && !base.Before(deployAt) {
			now = base
			if _, err := s.PutDeployment(ctx, app.ID, "", store.Marker{
				Version: "demo-2", Source: "ingest", ReportedAt: monitor.Stamp(now),
			}); err != nil {
				return err
			}
			failedCheck := check
			failedCheck.URL = strings.TrimSuffix(check.URL, "/healthy") + "/failing"
			current, err := s.Get(ctx, failing.ID)
			if err != nil {
				return err
			}
			if _, err := s.Patch(ctx, failing.ID, current.ConfigVersion, nil, &failedCheck, now); err != nil {
				return err
			}
			deployed = true
		}
		if !base.Before(gapStart) && base.Before(gapEnd) {
			continue // one deliberate asleep-machine gap, recorded by the next Tick
		}
		due := []struct {
			id string
			at time.Time
		}{
			{healthy.ID, monitor.GridSlot(healthy.ID, 900, base.Add(interval-time.Second))},
			{failing.ID, monitor.GridSlot(failing.ID, 900, base.Add(interval-time.Second))},
		}
		if due[1].at.Before(due[0].at) {
			due[0], due[1] = due[1], due[0]
		}
		for _, step := range due {
			if !step.at.Add(5 * time.Second).Before(realNow) {
				continue // never record a check after seed start
			}
			now = step.at.Add(time.Second)
			if _, _, _, err := s.Tick(ctx, now); err != nil {
				return fmt.Errorf("demo tick at %s: %w", now, err)
			}
			age := step.at.Sub(scene)
			outcome, duration := "healthy", int64(40)
			if step.id == healthy.ID {
				switch {
				case age >= 2*24*time.Hour && age < 2*24*time.Hour+3*time.Hour:
					outcome, duration = "failing", 75
				case age >= 3*24*time.Hour && age < 3*24*time.Hour+3*time.Hour:
					duration = 2200
				}
			} else if deployed {
				outcome, duration = "failing", 115
			}
			if err := record(step.id, step.at, outcome, duration); err != nil {
				return fmt.Errorf("demo check %s at %s: %w", step.id, step.at, err)
			}
			if err := deliver(); err != nil {
				return fmt.Errorf("demo delivery at %s: %w", now, err)
			}
		}
	}
	token := os.Getenv("STATUSFORGE_SAMPLE_JOB_TOKEN")
	if token == "" {
		token, err = heartbeat.Generate()
		if err != nil {
			return err
		}
	}
	hb := monitor.New("Demo heartbeat · late and missed", check, scene)
	hb.Kind = "heartbeat"
	hb.Check = monitor.Check{}
	hb.IntervalSeconds = 0
	hb.Heartbeat = &heartbeat.Configuration{
		Schedule: heartbeat.Schedule{IntervalSeconds: 900, GraceSeconds: 60},
		Token: &heartbeat.Token{
			Hash: heartbeat.Hash(
				token,
			), Hint: heartbeat.Hint(token), CreatedAt: monitor.Stamp(scene),
		},
		IngestPath: "/ingest/heartbeats/" + hb.ID,
	}
	expectation := heartbeat.Expect(scene, hb.Heartbeat.Schedule)
	hb.Expectation = &expectation
	now = scene
	if err := s.Create(ctx, hb); err != nil {
		return err
	}
	if _, err := s.SetApplication(ctx, hb.ID, app.ID); err != nil {
		return err
	}
	for index := 0; ; index++ {
		current, err := s.Get(ctx, hb.ID)
		if err != nil {
			return err
		}
		dueAt, err := time.Parse(time.RFC3339Nano, current.Expectation.DueAt)
		if err != nil {
			return err
		}
		if !dueAt.Add(61 * time.Second).Before(last) {
			break
		}
		switch {
		case index > 0 && index%200 == 100:
			now = dueAt.Add(61 * time.Second)
			live, err := s.WriteLiveness(ctx, now, time.Hour)
			if err != nil {
				return err
			}
			if err := s.Deadline(ctx, current, now, live); err != nil {
				return err
			}
		default:
			now = dueAt.Add(-10 * time.Second)
			if index > 0 && index%200 == 90 {
				now = dueAt.Add(30 * time.Second)
			}
			if _, err := s.WriteLiveness(ctx, now, time.Hour); err != nil {
				return err
			}
			if _, err := s.RecordHeartbeat(ctx, hb.ID, heartbeat.Hash(token), heartbeat.Report{}, now); err != nil {
				return err
			}
		}
	}
	now = last
	if _, err := s.WriteLiveness(ctx, now, time.Hour); err != nil {
		return err
	}
	for _, m := range []monitor.Monitor{healthy, failing} {
		current, err := s.Get(ctx, m.ID)
		if err != nil {
			return err
		}
		sum, err := s.Summary(ctx, current, "7d", realNow)
		if err != nil {
			return err
		}
		if sum.Coverage.Recorded*100 < sum.Coverage.Expected*95 ||
			sum.Coverage.NotObserved == 0 ||
			sum.Coverage.Expected != sum.Coverage.Recorded+sum.Coverage.NotObserved {
			return fmt.Errorf("inconsistent demo summary for %s: %+v", m.Name, sum.Coverage)
		}
	}
	current, err := s.Get(ctx, hb.ID)
	if err != nil {
		return err
	}
	hbSummary, err := s.HeartbeatSummary(ctx, current, "7d", realNow)
	if err != nil {
		return err
	}
	if hbSummary.Coverage.Recorded*100 < hbSummary.Coverage.Expected*95 ||
		hbSummary.Coverage.Outcomes.OnTime <= hbSummary.Coverage.Outcomes.Late ||
		hbSummary.Coverage.Outcomes.Late == 0 || hbSummary.Coverage.Outcomes.Missed == 0 ||
		hbSummary.Coverage.Expected != hbSummary.Coverage.Recorded+hbSummary.Coverage.NotObserved {
		return fmt.Errorf("inconsistent demo heartbeat summary: %+v", hbSummary.Coverage)
	}
	windows, err := s.ListMaintenance(ctx, healthy.ID, 10, realNow)
	if err != nil || len(windows) != 1 {
		return fmt.Errorf("demo maintenance missing: %d windows, %w", len(windows), err)
	}
	current, err = s.Get(ctx, healthy.ID)
	if err != nil {
		return err
	}
	healthySummary, err := s.Summary(ctx, current, "7d", realNow)
	if err != nil || healthySummary.Coverage.Maintenance == 0 {
		return fmt.Errorf("demo maintenance slots missing: %+v %w", healthySummary.Coverage, err)
	}
	markers, err := s.Deployments(ctx, app.ID, 10)
	if err != nil || len(markers) != 2 {
		return fmt.Errorf("demo deployments missing: %d markers, %w", len(markers), err)
	}
	application, err := s.Application(ctx, app.ID)
	if err != nil || len(application.Members) != 3 {
		return fmt.Errorf(
			"demo application members missing: %d members, %w",
			len(application.Members),
			err,
		)
	}
	incidents, err := s.ListIncidents(ctx, "", "", 100, realNow)
	if err != nil {
		return err
	}
	open, resolved, delivered, failed := false, false, false, false
	for _, in := range incidents {
		open = open || in.State == "open"
		resolved = resolved || in.State == "resolved"
		delivered = delivered || in.NotificationSummary.Delivered > 0
		failed = failed || in.NotificationSummary.Failed > 0
	}
	if !open || !resolved || !delivered || !failed {
		return fmt.Errorf("demo incidents incomplete: open=%t resolved=%t delivered=%t failed=%t",
			open, resolved, delivered, failed)
	}
	if err := s.MarkDemo(ctx); err != nil {
		return err
	}
	fmt.Printf(
		"demo seeded table=statusforge_demo heartbeat=%s monitors=%d incidents=%d seed_seconds=%.2f (open and resolved), 7-day summaries verified\n",
		hb.ID,
		3,
		len(incidents),
		time.Since(seedStarted).Seconds(),
	)
	return nil
}
