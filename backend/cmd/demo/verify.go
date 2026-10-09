package main

import (
	"fmt"
	"maps"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
	"github.com/lavinhoque33/statusforge/backend/internal/summary"
)

// verify reads the seeded week back through the store and fails unless every
// designed feature is present. It returns the number of incidents.
func (d *demo) verify(heartbeat monitor.Monitor) (int, error) {
	sums := map[*service]summary.Result{}
	for _, svc := range d.services() {
		current, err := d.s.Get(d.ctx, svc.ID)
		if err != nil {
			return 0, err
		}
		for _, window := range []string{"24h", "7d"} {
			sum, err := d.s.Summary(d.ctx, current, window, d.end)
			if err != nil {
				return 0, err
			}
			c := sum.Coverage
			if c.Recorded*100 < c.Expected*95 || c.Expected != c.Recorded+c.NotObserved {
				return 0, fmt.Errorf(
					"inconsistent demo %s summary for %s: %+v",
					window,
					svc.Name,
					c,
				)
			}
			if svc != d.worker && !varied(sum) {
				return 0, fmt.Errorf("demo %s latency for %s is not varied", window, svc.Name)
			}
			if window == "7d" {
				sums[svc] = sum
			}
		}
	}
	recoveries, incident, curve, worker := sums[d.recoveries], sums[d.incident], sums[d.curve], sums[d.worker]
	for _, check := range []struct {
		ok   bool
		what string
	}{
		{recoveries.Coverage.NotObserved > 0 && incident.Coverage.NotObserved > 0 &&
			curve.Coverage.NotObserved > 0, "receive gap"},
		{recoveries.Coverage.Maintenance > 0, "maintenance slots"},
		{recoveries.Latency.NoResponse > 0 && curve.Latency.NoResponse > 0 &&
			incident.Latency.NoResponse > 0, "no-response checks"},
		{curve.Coverage.Outcomes.CheckerProblem == checkerProblemSlots &&
			curve.Latency.CheckerProblems == checkerProblemSlots, "checker-problem burst"},
		{curve.Coverage.PausedSeconds > 0, "historical pause"},
		{worker.Coverage.PausedSeconds > 0, "paused worker"},
	} {
		if !check.ok {
			return 0, fmt.Errorf("demo %s missing", check.what)
		}
	}
	current, err := d.s.Get(d.ctx, d.worker.ID)
	if err != nil || current.Lifecycle != "paused" {
		return 0, fmt.Errorf("demo worker not paused: %q %w", current.Lifecycle, err)
	}
	current, err = d.s.Get(d.ctx, heartbeat.ID)
	if err != nil {
		return 0, err
	}
	hb, err := d.s.HeartbeatSummary(d.ctx, current, "7d", d.end)
	if err != nil {
		return 0, err
	}
	if hb.Coverage.Recorded*100 < hb.Coverage.Expected*95 ||
		hb.Coverage.Outcomes.OnTime <= hb.Coverage.Outcomes.Late ||
		hb.Coverage.Outcomes.Late == 0 || hb.Coverage.Outcomes.Missed == 0 ||
		hb.Coverage.Expected != hb.Coverage.Recorded+hb.Coverage.NotObserved {
		return 0, fmt.Errorf("inconsistent demo heartbeat summary: %+v", hb.Coverage)
	}
	windows, err := d.s.ListMaintenance(d.ctx, d.recoveries.ID, 10, d.end)
	if err != nil || len(windows) != 1 {
		return 0, fmt.Errorf("demo maintenance missing: %d windows, %w", len(windows), err)
	}
	markers, err := d.s.Deployments(d.ctx, d.app.ID, 10)
	if err != nil || len(markers) != 2 {
		return 0, fmt.Errorf("demo deployments missing: %d markers, %w", len(markers), err)
	}
	application, err := d.s.Application(d.ctx, d.app.ID)
	if err != nil || len(application.Members) != len(d.services())+1 {
		return 0, fmt.Errorf(
			"demo application members missing: %d members, %w",
			len(application.Members),
			err,
		)
	}
	incidents, err := d.s.ListIncidents(d.ctx, "", "", 100, d.end)
	if err != nil {
		return 0, err
	}
	// Exactly two incidents: isolated failures, timeouts and heartbeat misses stay below the threshold.
	open, resolved := false, false
	for _, in := range incidents {
		open = open || in.State == "open" && in.MonitorID == d.incident.ID
		resolved = resolved || in.State == "resolved" && in.MonitorID == d.recoveries.ID
		if err := d.verifyNotifications(in); err != nil {
			return 0, err
		}
	}
	if len(incidents) != 2 || !open || !resolved {
		return 0, fmt.Errorf("demo incidents incomplete: count=%d open=%t resolved=%t",
			len(incidents), open, resolved)
	}
	return len(incidents), nil
}

// verifyNotifications requires a delivered opened notice and delivered reminders
// for every incident. A resolved incident also needs its rejected recovery
// notice; an open incident needs a reminder once it has been open for a full interval.
func (d *demo) verifyNotifications(in store.Incident) error {
	_, _, _, notes, err := d.s.IncidentDetail(d.ctx, in.MonitorID, in.ID, d.end)
	if err != nil {
		return err
	}
	count := map[string]int{}
	for _, n := range notes {
		count[n.Kind+" "+n.State]++
	}
	reminders := count["reminder delivered"]
	want := map[string]int{"opened delivered": 1}
	if reminders > 0 {
		want["reminder delivered"] = reminders
	}
	if in.State == "resolved" {
		want["resolved failed"] = 1
	} else if opened, err := time.Parse(time.RFC3339Nano, in.OpenedAt); err != nil ||
		reminders == 0 && d.incident.last.Sub(opened) >= d.reminder {
		return fmt.Errorf("demo open incident has no delivered reminder: %v %w", count, err)
	}
	if !maps.Equal(count, want) {
		return fmt.Errorf("demo %s incident notifications: %v", in.State, count)
	}
	return nil
}

// varied reports whether at least half of the buckets with a median have distinct
// medians and maxima, so the latency chart draws a shape rather than a flat line.
func varied(sum summary.Result) bool {
	medians, maxima := map[int64]bool{}, map[int64]bool{}
	buckets := 0
	for _, b := range sum.Buckets {
		if b.Latency.MedianMs == nil || b.Latency.MaxMs == nil {
			continue
		}
		buckets++
		medians[*b.Latency.MedianMs] = true
		maxima[*b.Latency.MaxMs] = true
	}
	return buckets >= 4 && len(medians)*2 >= buckets && len(maxima)*2 >= buckets
}
