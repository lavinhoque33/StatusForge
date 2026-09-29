package scheduler

import (
	"testing"
	"time"
)

func TestCoverageStates(t *testing.T) {
	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	c := NewCoverage(4, true)
	c.start(start)
	check := func(now time.Time, state string, due, missed int) {
		t.Helper()
		got := c.Snapshot(now)
		if got.State != state || got.DueChecks != due || got.MissedChecks != missed ||
			got.WindowMinutes != 5 || got.Workers != 4 {
			t.Fatalf("at %s: %+v, want %s %d/%d", now.Sub(start), got, state, missed, due)
		}
	}
	check(start, "unknown", 0, 0)
	// A slot that expired before this run started is not this run's evidence.
	c.record(start.Add(time.Second), at(start.Add(-time.Minute)), true)
	check(start.Add(time.Second), "unknown", 0, 0)
	for i := range 20 {
		c.record(start.Add(time.Duration(i)*time.Second), at(start), false)
	}
	check(start.Add(20*time.Second), "ok", 20, 0)
	// One miss in 21 (4.8%) is within the 95% target; two in 22 are not.
	c.record(start.Add(30*time.Second), at(start.Add(20*time.Second)), true)
	check(start.Add(30*time.Second), "ok", 21, 1)
	c.record(start.Add(31*time.Second), at(start.Add(21*time.Second)), true)
	check(start.Add(31*time.Second), "behind", 22, 2)
	// Evidence older than the window no longer counts.
	later := start.Add(31*time.Second + CoverageWindow)
	check(later, "unknown", 0, 0)
	c.record(later, at(later), false)
	check(later, "ok", 1, 0)

	disabled := NewCoverage(2, false)
	disabled.start(start)
	disabled.record(start, at(start), true)
	if got := disabled.Snapshot(start); got.State != "disabled" || got.Workers != 2 {
		t.Fatalf("disabled %+v", got)
	}
}
